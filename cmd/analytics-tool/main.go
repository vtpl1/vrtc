// analytics-tool is an analytics simulation tool that:
//  1. Lists all cameras on a vrtc edge instance via GET /api/cameras.
//  2. Subscribes to each camera's WebSocket stream (/api/cameras/ws/stream) to
//     receive fMP4 fragments and extract the avgrabber wall-clock from timing
//     text frames ({"type":"timing","wallClock":"..."}) emitted by the MSE muxer.
//  3. Simulates an AI inference pipeline with a random 100ms–5s latency per frame.
//  4. Pushes FrameAnalytics results back to edge over the gRPC
//     AnalyticsIngestionService (muxed on the same port via cmux).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/vtpl1/vrtc-sdk/av"
	pb "github.com/vtpl1/vrtc-sdk/av/format/grpc/gen/avtransportv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ── flags ──────────────────────────────────────────────────────────────────────

var (
	flagEdgeURL    string
	flagMinLatency time.Duration
	flagMaxLatency time.Duration
)

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	root := &cobra.Command{
		Use:   "analytics-tool",
		Short: "Simulates an AI analytics engine for vrtc edge",
		RunE:  run,
	}

	root.Flags().StringVar(&flagEdgeURL, "edge-url", "http://localhost:8080",
		"vrtc edge base URL (http:// or ws:// are both accepted)")
	root.Flags().DurationVar(&flagMinLatency, "min-latency", 100*time.Millisecond,
		"Minimum simulated analytics processing latency")
	root.Flags().DurationVar(&flagMaxLatency, "max-latency", 5*time.Second,
		"Maximum simulated analytics processing latency")

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── runner ────────────────────────────────────────────────────────────────────

func run(_ *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	httpBase := normaliseHTTP(flagEdgeURL)
	grpcTarget := grpcTargetFromURL(flagEdgeURL)

	log.Info().
		Str("edgeURL", httpBase).
		Str("grpcTarget", grpcTarget).
		Dur("minLatency", flagMinLatency).
		Dur("maxLatency", flagMaxLatency).
		Msg("analytics-tool starting")

	// ── gRPC connection ────────────────────────────────────────────────────
	conn, err := grpc.NewClient(grpcTarget,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dial gRPC %s: %w", grpcTarget, err)
	}

	defer conn.Close()

	client := pb.NewAnalyticsIngestionServiceClient(conn)

	stream, err := client.IngestAnalytics(ctx)
	if err != nil {
		return fmt.Errorf("open IngestAnalytics stream: %w", err)
	}

	// sendMu serialises sends on the single shared gRPC stream.
	var sendMu sync.Mutex

	sendAnalytics := func(req *pb.IngestAnalyticsRequest) {
		sendMu.Lock()
		defer sendMu.Unlock()

		if serr := stream.Send(req); serr != nil {
			log.Warn().Err(serr).Str("sourceId", req.GetSourceId()).Msg("gRPC send error")
		}
	}

	// ── list cameras ───────────────────────────────────────────────────────
	cameras, err := listCameras(ctx, httpBase)
	if err != nil {
		return fmt.Errorf("list cameras: %w", err)
	}

	if len(cameras) == 0 {
		log.Warn().Msg("no cameras found — nothing to do")

		return nil
	}

	log.Info().Int("count", len(cameras)).Msg("cameras discovered")

	// ── per-camera goroutines ──────────────────────────────────────────────
	var wg sync.WaitGroup

	for i := range cameras {
		cam := cameras[i]

		wg.Go(func() {
			runCamera(ctx, httpBase, cam, sendAnalytics)
		})
	}

	<-ctx.Done()
	log.Info().Msg("shutdown signal received")

	_ = stream.CloseSend()

	wg.Wait()

	return nil
}

// ── camera worker ──────────────────────────────────────────────────────────────

// frameWork holds a captured frame's wall-clock waiting for simulated inference.
type frameWork struct {
	sourceID  string
	wallClock time.Time
}

func runCamera(
	ctx context.Context,
	httpBase string,
	cam cameraEntry,
	send func(*pb.IngestAnalyticsRequest),
) {
	wsURL := wsStreamURL(httpBase, cam.CameraID)
	log.Info().Str("cameraId", cam.CameraID).Str("wsURL", wsURL).Msg("connecting to camera stream")

	// Buffered work channel: WS receiver → inference worker.
	work := make(chan frameWork, 32)

	var wg sync.WaitGroup

	// Inference worker: sleep to simulate latency, then send analytics.
	wg.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case fw, ok := <-work:
				if !ok {
					return
				}

				sleepDur := randomDuration(flagMinLatency, flagMaxLatency)

				select {
				case <-ctx.Done():
					return
				case <-time.After(sleepDur):
				}

				inferDoneMs := time.Now().UnixMilli()
				fa := generateAnalytics(fw.wallClock, inferDoneMs)
				req := buildRequest(fw.sourceID, fw.wallClock, fa)

				send(req)

				log.Debug().
					Str("cameraId", fw.sourceID).
					Int64("captureMs", fw.wallClock.UnixMilli()).
					Int64("inferenceMs", inferDoneMs).
					Int32("people", fa.PeopleCount).
					Int32("vehicles", fa.VehicleCount).
					Msg("analytics pushed")
			}
		}
	})

	// WS receiver: connect to edge stream and feed frame work items.
	wg.Go(func() {
		defer close(work)

		subscribeStream(ctx, wsURL, cam.CameraID, work)
	})

	wg.Wait()
}

// ── WS stream subscriber ───────────────────────────────────────────────────────

const retryDelay = 3 * time.Second

func subscribeStream(ctx context.Context, wsURL, cameraID string, work chan<- frameWork) {
	for {
		if ctx.Err() != nil {
			return
		}

		if err := connectAndRead(ctx, wsURL, cameraID, work); err != nil {
			log.Warn().Err(err).Str("cameraId", cameraID).Dur("retry", retryDelay).
				Msg("stream disconnected, retrying")

			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
		}
	}
}

// wsTextFrame is a partial parse of any text frame from the WS stream.
// All timing frames (mode_change, playback_info, seeked, timing) carry
// wallClock as an RFC3339 string with millisecond precision.
type wsTextFrame struct {
	Type      string `json:"type"`
	WallClock string `json:"wallClock,omitempty"` // RFC3339Milli — from mode_change / playback_info / seeked / timing
}

func connectAndRead(ctx context.Context, wsURL, cameraID string, work chan<- frameWork) error {
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {"http://localhost"}},
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	defer func() { _ = conn.CloseNow() }()

	// Send {"type":"mse"} to initiate streaming.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"mse"}`)); err != nil {
		return fmt.Errorf("send mse command: %w", err)
	}

	var latestWallClock time.Time // updated from text frames

	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}

		switch msgType {
		case websocket.MessageText:
			var frame wsTextFrame
			if jerr := json.Unmarshal(data, &frame); jerr != nil {
				continue
			}

			// All relevant frame types carry wallClock as RFC3339Milli.
			if frame.WallClock == "" {
				continue
			}

			switch frame.Type {
			case "mode_change", "playback_info", "seeked", "timing":
			default:
				continue
			}

			parsed, perr := time.Parse(av.RFC3339Milli, frame.WallClock)
			if perr != nil {
				continue
			}

			if !latestWallClock.IsZero() && parsed.Before(latestWallClock) {
				log.Warn().Str("cameraId", cameraID).
					Time("previous", latestWallClock).
					Time("received", parsed).
					Msg("wall-clock went backwards, accepting new value")
			}

			latestWallClock = parsed

		case websocket.MessageBinary:
			// Binary frame = fMP4 fragment. Enqueue with current wall-clock reference.
			if latestWallClock.IsZero() {
				continue // no wall-clock yet; skip until first timing frame arrives
			}

			select {
			case work <- frameWork{
				sourceID:  cameraID,
				wallClock: latestWallClock,
			}:
			default:
				log.Debug().Str("cameraId", cameraID).Msg("work queue full, frame skipped")
			}
		}
	}
}

// ── analytics generation ───────────────────────────────────────────────────────

func generateAnalytics(wallClock time.Time, inferenceDoneMs int64) *av.FrameAnalytics {
	//nolint:gosec // rand used for simulation only, not security
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	people := int32(r.Intn(5))
	vehicles := int32(r.Intn(3))

	captureMs := wallClock.UnixMilli()

	fa := &av.FrameAnalytics{
		CaptureMS:    captureMs,
		CaptureEndMS: captureMs, // single-frame capture; start == end
		InferenceMS:  inferenceDoneMs,
		PeopleCount:  people,
		VehicleCount: vehicles,
	}

	numObjects := r.Intn(4) // 0–3 detections

	for range numObjects {
		classID := uint32(r.Intn(2)) // 0=person, 1=vehicle

		fa.Objects = append(fa.Objects, &av.Detection{
			X:          uint32(r.Intn(1920)),
			Y:          uint32(r.Intn(1080)),
			W:          uint32(50 + r.Intn(200)),
			H:          uint32(50 + r.Intn(200)),
			ClassID:    classID,
			Confidence: uint32(50 + r.Intn(50)), // 50–99
			TrackID:    int64(r.Intn(100)),
		})
	}

	return fa
}

func buildRequest(
	sourceID string,
	wallClock time.Time,
	fa *av.FrameAnalytics,
) *pb.IngestAnalyticsRequest {
	protoFA := &pb.FrameAnalytics{
		CaptureMs:    fa.CaptureMS,
		CaptureEndMs: fa.CaptureEndMS,
		InferenceMs:  fa.InferenceMS,
		PeopleCount:  fa.PeopleCount,
		VehicleCount: fa.VehicleCount,
	}

	for _, d := range fa.Objects {
		if d == nil {
			continue
		}

		protoFA.Objects = append(protoFA.Objects, &pb.Detection{
			X: d.X, Y: d.Y, W: d.W, H: d.H,
			ClassId:    d.ClassID,
			Confidence: d.Confidence,
			TrackId:    d.TrackID,
			IsEvent:    d.IsEvent,
		})
	}

	return &pb.IngestAnalyticsRequest{
		SourceId:    sourceID,
		WallClockMs: wallClock.UnixMilli(),
		Analytics:   protoFA,
	}
}

// ── camera listing ─────────────────────────────────────────────────────────────

type cameraEntry struct {
	CameraID string `json:"cameraId"`
	Name     string `json:"name"`
}

func listCameras(ctx context.Context, httpBase string) ([]cameraEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpBase+"/api/cameras", nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/cameras returned %s", resp.Status)
	}

	// Huma wraps the response body in {"$schema":..., "items":[...]}.
	var body struct {
		Items []cameraEntry `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode cameras: %w", err)
	}

	return body.Items, nil
}

// ── URL helpers ────────────────────────────────────────────────────────────────

// normaliseHTTP ensures the URL uses the http(s):// scheme.
func normaliseHTTP(u string) string {
	switch {
	case strings.HasPrefix(u, "ws://"):
		return "http://" + u[5:]
	case strings.HasPrefix(u, "wss://"):
		return "https://" + u[6:]
	default:
		return u
	}
}

// grpcTargetFromURL extracts host:port for use as a gRPC dial target.
func grpcTargetFromURL(u string) string {
	u = normaliseHTTP(u)
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "https://")

	if i := strings.Index(u, "/"); i >= 0 {
		u = u[:i]
	}

	return u
}

// wsStreamURL builds the WebSocket stream URL for a given camera.
func wsStreamURL(httpBase, cameraID string) string {
	base := strings.TrimRight(httpBase, "/")
	base = strings.ReplaceAll(base, "http://", "ws://")
	base = strings.ReplaceAll(base, "https://", "wss://")

	return base + "/api/cameras/ws/stream?cameraId=" + cameraID
}

// randomDuration returns a uniformly random duration in [min, max].
func randomDuration(minD, maxD time.Duration) time.Duration {
	if maxD <= minD {
		return minD
	}

	//nolint:gosec // rand used for simulation only
	return minD + time.Duration(rand.Int63n(int64(maxD-minD)))
}
