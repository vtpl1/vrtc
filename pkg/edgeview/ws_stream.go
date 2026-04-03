package edgeview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/format/mse"
	"github.com/vtpl1/vrtc-sdk/av/relayhub"
)

var (
	errCameraIDRequired  = errors.New("camera_id is required")
	errNoPlaybackSession = errors.New("no playback session")
)

// wsCommand is a JSON command from the WebSocket client.
type wsCommand struct {
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
	Time  string `json:"time,omitempty"` // RFC3339 timestamp for seek
}

// wsStream handles the unified /ws/stream WebSocket endpoint.
//
// Protocol:
//   - Client connects with query params: camera_id, start (RFC3339, optional)
//   - No start param → live mode (attach to main hub, multi-consumer fan-out)
//   - start param → recorded mode (per-session relay, single consumer)
//   - Client sends JSON: {"type":"mse"} to start streaming
//   - Server sends text: {"type":"mse","value":"video/mp4; codecs=\"...\""}
//   - Server sends binary: fMP4 init segment, then continuous moof+mdat fragments
//   - Client may send {"type":"mse","value":"pause"} or {"type":"mse","value":"resume"}
//   - Client may send {"type":"mse","value":"seek","time":"RFC3339"} to seek
//   - Seeking to the future switches to live; seeking to the past switches to recorded
//
//nolint:funlen // WebSocket handler -- lifecycle complexity is inherent.
func (h *HTTPHandler) wsStream(w http.ResponseWriter, r *http.Request) {
	cameraID, err := parseWSCameraID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	start := parseWSOptionalTime(r, "start")

	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  []string{"*"},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		h.log.Error().Err(err).Msg("ws stream: accept failed")

		return
	}

	defer func() { _ = wsConn.CloseNow() }()

	ctx := r.Context()

	session := &streamSession{
		handler:  h,
		wsConn:   wsConn,
		cameraID: cameraID,
	}

	if err := session.start(ctx, start); err != nil {
		writeWSErrorResponse(ctx, wsConn, err, "stream start failed")

		return
	}

	defer session.stop(ctx)

	errChan := make(chan error, 1)

	rCtx, rCancel := context.WithCancel(ctx)
	defer rCancel()

	var wg sync.WaitGroup

	wg.Go(func() {
		for {
			select {
			case <-rCtx.Done():
				return
			default:
				cmd, rerr := readWSCommand(rCtx, wsConn)
				if rerr != nil {
					if !errors.Is(rerr, context.Canceled) &&
						!errors.Is(rerr, context.DeadlineExceeded) {
						errChan <- rerr
					}

					return
				}

				if cmd.Type != "mse" {
					continue
				}

				switch cmd.Value {
				case "": // initial play
					if serr := session.attachConsumer(ctx, errChan); serr != nil {
						writeWSErrorResponse(ctx, wsConn, serr, "consume failed")

						errChan <- serr

						return
					}
				case "pause":
					session.pause(ctx)
				case "resume":
					session.resume(ctx)
				case "seek":
					seekTime, perr := time.Parse(time.RFC3339, cmd.Time)
					if perr != nil {
						_ = wsjson.Write(ctx, wsConn, map[string]string{
							"type":  "error",
							"error": "invalid seek time, expected RFC3339",
						})

						continue
					}

					session.stop(ctx)

					if serr := session.start(ctx, seekTime); serr != nil {
						writeWSErrorResponse(ctx, wsConn, serr, "seek failed")

						errChan <- serr

						return
					}

					if serr := session.attachConsumer(ctx, errChan); serr != nil {
						writeWSErrorResponse(ctx, wsConn, serr, "consume after seek failed")

						errChan <- serr

						return
					}
				}
			}
		}
	})

	select {
	case <-ctx.Done():
	case <-errChan:
	}

	rCancel()
}

// streamSession manages a single streaming pipeline. It transparently handles
// both live mode (consumer on the main hub) and recorded mode (per-session
// relay hub). Seek commands can transition between modes.
type streamSession struct {
	handler  *HTTPHandler
	wsConn   *websocket.Conn
	cameraID string

	mu         sync.Mutex
	playSM     *relayhub.RelayHub // non-nil when in recorded mode
	ms         *mse.MSEWriter
	consumer   av.ConsumerHandle // non-nil when in recorded mode
	liveMode   bool              // true when attached to the live hub
	liveHandle av.ConsumerHandle // non-nil when in live mode
	untrack    func()            // decrements viewer count; non-nil when live
}

// start resolves the playback mode and prepares the session.
// A zero from time means pure live mode.
func (s *streamSession) start(ctx context.Context, from time.Time) error {
	if from.IsZero() {
		return s.startLive(ctx)
	}

	return s.startResolved(ctx, from)
}

// startLive sets up a pure live session (no recording index needed).
func (s *streamSession) startLive(ctx context.Context) error {
	_ = wsjson.Write(ctx, s.wsConn, map[string]string{
		"type": "mode_change",
		"mode": "live",
	})

	s.mu.Lock()
	s.liveMode = true
	s.playSM = nil
	s.ms = nil
	s.consumer = nil
	s.liveHandle = nil
	s.untrack = nil
	s.mu.Unlock()

	return nil
}

// startResolved uses ResolvePlaybackStart to determine the mode.
func (s *streamSession) startResolved(ctx context.Context, from time.Time) error {
	resolvedFrom, mode, err := s.handler.svc.ResolvePlaybackStart(
		ctx,
		s.cameraID,
		from,
		time.Time{},
	)
	if err != nil {
		return fmt.Errorf("resolve playback: %w", err)
	}

	switch mode {
	case PlaybackModeLive:
		return s.startLive(ctx)

	case PlaybackModeFirstAvailable:
		_ = wsjson.Write(ctx, s.wsConn, map[string]string{
			"type":        "playback_info",
			"actualStart": resolvedFrom.UTC().Format(time.RFC3339),
			"mode":        "first_available",
		})

	case PlaybackModeRecorded:
		_ = wsjson.Write(ctx, s.wsConn, map[string]string{
			"type":        "playback_info",
			"actualStart": resolvedFrom.UTC().Format(time.RFC3339),
			"mode":        "recorded",
		})
	}

	factory := s.handler.svc.RecordedDemuxerFactory(s.cameraID, resolvedFrom, time.Time{})
	sm := relayhub.New(factory, nil, relayhub.WithMaxConsumers(1))

	if err := sm.Start(ctx); err != nil {
		return fmt.Errorf("start playback: %w", err)
	}

	s.mu.Lock()
	s.playSM = sm
	s.liveMode = false
	s.liveHandle = nil
	s.untrack = nil
	s.ms = nil
	s.consumer = nil
	s.mu.Unlock()

	return nil
}

func (s *streamSession) attachConsumer(ctx context.Context, errChan chan<- error) error {
	s.mu.Lock()
	sm := s.playSM
	live := s.liveMode
	s.mu.Unlock()

	localMs, consumeOpts, err := s.buildMSEConsumer(ctx, errChan)
	if err != nil {
		return err
	}

	if live {
		return s.attachLiveConsumer(ctx, localMs, consumeOpts)
	}

	if sm == nil {
		_ = localMs.Close()

		return errNoPlaybackSession
	}

	handle, cerr := sm.Consume(ctx, s.cameraID, consumeOpts)
	if cerr != nil {
		_ = localMs.Close()

		return cerr
	}

	s.mu.Lock()
	s.ms = localMs
	s.consumer = handle
	s.mu.Unlock()

	return nil
}

// buildMSEConsumer creates an MSE writer and ConsumeOptions for WebSocket delivery.
func (s *streamSession) buildMSEConsumer(
	ctx context.Context,
	errChan chan<- error,
) (*mse.MSEWriter, av.ConsumeOptions, error) {
	binaryFactory := func() (io.WriteCloser, error) {
		return s.wsConn.Writer(ctx, websocket.MessageBinary)
	}
	textFactory := func() (io.WriteCloser, error) {
		return s.wsConn.Writer(ctx, websocket.MessageText)
	}

	localMs, err := mse.NewFromFactories(binaryFactory, textFactory)
	if err != nil {
		return nil, av.ConsumeOptions{}, err
	}

	opts := av.ConsumeOptions{
		MuxerFactory: func(_ context.Context, _ string) (av.MuxCloser, error) {
			return localMs, nil
		},
		MuxerRemover: func(_ context.Context, _ string) error {
			return localMs.Close()
		},
		ErrChan: errChan,
	}

	return localMs, opts, nil
}

// attachLiveConsumer attaches to the main live hub and tracks the viewer.
func (s *streamSession) attachLiveConsumer(
	ctx context.Context,
	localMs *mse.MSEWriter,
	opts av.ConsumeOptions,
) error {
	handle, cerr := s.handler.svc.Hub().Consume(ctx, s.cameraID, opts)
	if cerr != nil {
		_ = localMs.Close()

		return cerr
	}

	untrack := s.handler.svc.TrackConsumer()

	s.mu.Lock()
	s.ms = localMs
	s.liveHandle = handle
	s.untrack = untrack
	s.mu.Unlock()

	return nil
}

func (s *streamSession) pause(ctx context.Context) {
	s.mu.Lock()
	sm := s.playSM
	live := s.liveMode
	s.mu.Unlock()

	if live {
		_ = s.handler.svc.Hub().PauseRelay(ctx, s.cameraID)

		return
	}

	if sm != nil {
		_ = sm.PauseRelay(ctx, s.cameraID)
	}
}

func (s *streamSession) resume(ctx context.Context) {
	s.mu.Lock()
	sm := s.playSM
	live := s.liveMode
	s.mu.Unlock()

	if live {
		_ = s.handler.svc.Hub().ResumeRelay(ctx, s.cameraID)

		return
	}

	if sm != nil {
		_ = sm.ResumeRelay(ctx, s.cameraID)
	}
}

func (s *streamSession) stop(ctx context.Context) {
	s.mu.Lock()
	consumer := s.consumer
	liveHandle := s.liveHandle
	ms := s.ms
	sm := s.playSM
	untrack := s.untrack
	s.consumer = nil
	s.liveHandle = nil
	s.ms = nil
	s.playSM = nil
	s.liveMode = false
	s.untrack = nil
	s.mu.Unlock()

	if consumer != nil {
		_ = consumer.Close(ctx)
	}

	if liveHandle != nil {
		_ = liveHandle.Close(ctx)
	}

	if untrack != nil {
		untrack()
	}

	if ms != nil {
		_ = ms.Close()
	}

	if sm != nil {
		_ = sm.Stop()
	}
}

// ── Shared helpers ──────────────────────────────────────────────────────────

// readWSCommand reads and decodes a JSON command from the WebSocket.
func readWSCommand(ctx context.Context, wsConn *websocket.Conn) (wsCommand, error) {
	cmd := wsCommand{}
	err := wsjson.Read(ctx, wsConn, &cmd)

	return cmd, err
}

// writeWSErrorResponse sends an error JSON message over the WebSocket.
func writeWSErrorResponse(ctx context.Context, wsConn *websocket.Conn, err error, msg string) {
	errResponse := map[string]string{
		"type":  "error",
		"error": msg + ": " + err.Error(),
	}
	_ = wsjson.Write(ctx, wsConn, errResponse)
}

// parseWSCameraID extracts and validates the camera_id query parameter.
func parseWSCameraID(r *http.Request) (string, error) {
	id := r.URL.Query().Get("camera_id")
	if id == "" {
		return "", errCameraIDRequired
	}

	return id, nil
}

// parseWSOptionalTime reads an RFC3339 query parameter, returning zero if
// absent or unparseable.
func parseWSOptionalTime(r *http.Request, key string) time.Time {
	s := r.URL.Query().Get(key)
	if s == "" {
		return time.Time{}
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}

	return t
}
