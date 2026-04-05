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

const cmdTypeMSE = "mse"

var (
	errCameraIDRequired  = errors.New("camera_id is required")
	errNoPlaybackSession = errors.New("no playback session")
)

// wsCommand is a JSON command from the WebSocket client.
type wsCommand struct {
	Type   string  `json:"type"`
	Value  string  `json:"value,omitempty"`
	Time   string  `json:"time,omitempty"`   // RFC3339 timestamp for seek
	Seq    int64   `json:"seq,omitempty"`    // monotonic counter for seek debouncing
	Offset string  `json:"offset,omitempty"` // relative seek: "-30s", "60s", etc (Go duration)
	Rate   float64 `json:"rate,omitempty"`   // playback speed (reserved for future use)
}

// seekedResponse is sent to the client after a seek completes.
type seekedResponse struct {
	Type         string `json:"type"`             // always "seeked"
	WallClock    string `json:"wallClock"`        // actual wall-clock time landed on (RFC3339Milli)
	Mode         string `json:"mode"`             // "recorded", "live", "first_available"
	CodecChanged bool   `json:"codecChanged"`     // true if codecs differ from previous position
	Codecs       string `json:"codecs,omitempty"` // new MIME codec string (only when codecChanged)
	Gap          bool   `json:"gap,omitempty"`    // true if seek landed after a gap (snapped forward)
	Seq          int64  `json:"seq"`              // echoed from request
}

// wsStream handles the unified /ws/stream WebSocket endpoint.
//
// Protocol:
//   - Client connects with query params: cameraId, start (RFC3339, optional)
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
		writeProblem(w, http.StatusBadRequest, err.Error())

		return
	}

	start, err := parseWSOptionalTime(r, "start")
	if err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())

		return
	}

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

	analyticsMode := r.URL.Query().Get("analytics") == "true"

	session := &streamSession{
		handler:       h,
		wsConn:        wsConn,
		cameraID:      cameraID,
		analyticsMode: analyticsMode,
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

				if cmd.Type != cmdTypeMSE {
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
					if serr := session.handleSeek(ctx, cmd, errChan); serr != nil {
						writeWSErrorResponse(ctx, wsConn, serr, "seek failed")

						errChan <- serr

						return
					}
				case "skip":
					if serr := session.handleSkip(ctx, cmd, errChan); serr != nil {
						writeWSErrorResponse(ctx, wsConn, serr, "skip failed")

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

	analyticsMode bool // true when consuming from the analytics relay hub

	// Seek state.
	lastSeqSeen  int64     // highest seq received; used to discard stale seeks
	lastSeekTime time.Time // wall-clock time of the last successful seek target
	lastMode     string    // mode after last start/seek ("recorded", "live", "first_available")
	lastCodecStr string    // MIME codec string after last start (for codec-change detection)
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
	_ = wsjson.Write(ctx, s.wsConn, map[string]any{
		"type":      "mode_change",
		"mode":      "live",
		"wallClock": time.Now().UTC().Format(av.RFC3339Milli),
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
		_ = wsjson.Write(ctx, s.wsConn, map[string]any{
			"type":                 "playback_info",
			"actualStartWallClock": resolvedFrom.UTC().Format(av.RFC3339Milli),
			"wallClock":            resolvedFrom.UTC().Format(av.RFC3339Milli),
			"mode":                 "first_available",
		})

	case PlaybackModeRecorded:
		_ = wsjson.Write(ctx, s.wsConn, map[string]any{
			"type":                 "playback_info",
			"actualStartWallClock": resolvedFrom.UTC().Format(av.RFC3339Milli),
			"wallClock":            resolvedFrom.UTC().Format(av.RFC3339Milli),
			"mode":                 "recorded",
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
	s.lastSeekTime = resolvedFrom
	s.lastMode = mode
	s.mu.Unlock()

	return nil
}

func (s *streamSession) attachConsumer(ctx context.Context, errChan chan<- error) error {
	addStart := time.Now()

	s.mu.Lock()
	sm := s.playSM
	live := s.liveMode
	s.mu.Unlock()

	localMs, consumeOpts, err := s.buildMSEConsumer(ctx, errChan)
	if err != nil {
		return err
	}

	if live {
		if lerr := s.attachLiveConsumer(ctx, localMs, consumeOpts); lerr != nil {
			return lerr
		}

		if s.handler.collector != nil {
			s.handler.collector.RecordConsumerAdd(time.Since(addStart), s.cameraID)
		}

		return nil
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

	if s.handler.collector != nil {
		s.handler.collector.RecordConsumerAdd(time.Since(addStart), s.cameraID)
	}

	s.mu.Lock()
	s.ms = localMs
	s.consumer = handle
	s.mu.Unlock()

	return nil
}

// wsWriteTimeout is the maximum duration for a single WebSocket write.
// A stalled client that does not read will hit this deadline, causing the
// consumer to be closed rather than leaking a goroutine indefinitely.
const wsWriteTimeout = 10 * time.Second

// buildMSEConsumer creates an MSE writer and ConsumeOptions for WebSocket delivery.
func (s *streamSession) buildMSEConsumer(
	ctx context.Context,
	errChan chan<- error,
) (*mse.MSEWriter, av.ConsumeOptions, error) {
	binaryFactory := func() (io.WriteCloser, error) {
		writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)

		return &cancelWriter{
			WriteCloser: mustWriter(s.wsConn.Writer(writeCtx, websocket.MessageBinary)),
			cancel:      cancel,
		}, nil
	}
	textFactory := func() (io.WriteCloser, error) {
		writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)

		return &cancelWriter{
			WriteCloser: mustWriter(s.wsConn.Writer(writeCtx, websocket.MessageText)),
			cancel:      cancel,
		}, nil
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
	hub := s.handler.svc.Hub()
	if s.analyticsMode && s.handler.svc.analyticsRelayHub != nil {
		hub = s.handler.svc.analyticsRelayHub
	}

	handle, cerr := hub.Consume(ctx, s.cameraID, opts)
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

	// Live mode: do NOT pause the shared relay — it would stop packets for
	// all consumers (including the recorder). The client-side MSE player
	// handles pause locally.
	if live {
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

// handleSeek processes an absolute seek command. It stops the current session,
// resolves the target time, starts a new session, and sends a seekedResponse.
func (s *streamSession) handleSeek(ctx context.Context, cmd wsCommand, errChan chan<- error) error {
	// Seq-based debounce: discard stale seeks.
	s.mu.Lock()
	if cmd.Seq > 0 && cmd.Seq <= s.lastSeqSeen {
		s.mu.Unlock()

		return nil // superseded by a newer seek
	}

	if cmd.Seq > 0 {
		s.lastSeqSeen = cmd.Seq
	}

	prevCodecStr := s.lastCodecStr
	s.mu.Unlock()

	// Special value "now" means switch to live.
	if cmd.Time == "now" {
		s.stop(ctx)

		if err := s.startLive(ctx); err != nil {
			return err
		}

		if err := s.attachConsumer(ctx, errChan); err != nil {
			return err
		}

		_ = wsjson.Write(ctx, s.wsConn, seekedResponse{
			Type:         "seeked",
			WallClock:    time.Now().UTC().Format(av.RFC3339Milli),
			Mode:         PlaybackModeLive,
			CodecChanged: false,
			Seq:          cmd.Seq,
		})

		return nil
	}

	seekTime, perr := time.Parse(time.RFC3339, cmd.Time)
	if perr != nil {
		_ = wsjson.Write(ctx, s.wsConn, map[string]string{
			"type":  "error",
			"error": "invalid seek time, expected RFC3339 or \"now\"",
		})

		return nil //nolint:nilerr // not fatal — client can retry with valid time
	}

	return s.executeSeek(ctx, seekTime, cmd.Seq, prevCodecStr, errChan)
}

// handleSkip processes a relative seek command. It computes the absolute target
// from the current position + offset, then delegates to executeSeek.
func (s *streamSession) handleSkip(ctx context.Context, cmd wsCommand, errChan chan<- error) error {
	offset, perr := time.ParseDuration(cmd.Offset)
	if perr != nil {
		_ = wsjson.Write(ctx, s.wsConn, map[string]string{
			"type":  "error",
			"error": "invalid offset, expected Go duration (e.g. \"-30s\", \"60s\")",
		})

		return nil //nolint:nilerr // not fatal — client can retry with valid offset
	}

	s.mu.Lock()
	base := s.lastSeekTime
	prevCodecStr := s.lastCodecStr

	if cmd.Seq > 0 && cmd.Seq <= s.lastSeqSeen {
		s.mu.Unlock()

		return nil
	}

	if cmd.Seq > 0 {
		s.lastSeqSeen = cmd.Seq
	}
	s.mu.Unlock()

	if base.IsZero() {
		base = time.Now()
	}

	target := base.Add(offset)

	return s.executeSeek(ctx, target, cmd.Seq, prevCodecStr, errChan)
}

// executeSeek performs the stop → resolve → start → attach cycle for a seek
// and sends the appropriate seekedResponse to the client.
func (s *streamSession) executeSeek(
	ctx context.Context,
	seekTime time.Time,
	seq int64,
	prevCodecStr string,
	errChan chan<- error,
) error {
	seekStart := time.Now()

	s.stop(ctx)

	resolvedFrom, mode, gap, err := s.resolveAndStartSeek(ctx, seekTime)
	if err != nil {
		return err
	}

	// Check for stale seq before the expensive attach step.
	s.mu.Lock()
	if seq > 0 && seq < s.lastSeqSeen {
		s.mu.Unlock()
		s.stop(ctx)

		return nil
	}
	s.mu.Unlock()

	if err := s.attachConsumer(ctx, errChan); err != nil {
		return err
	}

	if s.handler.collector != nil {
		s.handler.collector.RecordSeekLatency(time.Since(seekStart), s.cameraID)
	}

	// Determine if codecs changed and send the response.
	newCodecStr := s.captureCodecStr()
	codecChanged := prevCodecStr != "" && newCodecStr != "" && prevCodecStr != newCodecStr

	s.sendSeekedResponse(ctx, resolvedFrom, mode, gap, codecChanged, newCodecStr, seq)

	// Update seek state.
	s.mu.Lock()

	actualTime := resolvedFrom
	if mode == PlaybackModeLive {
		actualTime = time.Now()
	}

	s.lastSeekTime = actualTime
	s.lastMode = mode
	s.lastCodecStr = newCodecStr
	s.mu.Unlock()

	return nil
}

// resolveAndStartSeek resolves the playback mode for the given time, starts
// the appropriate session, and returns the resolved time, mode, and gap flag.
func (s *streamSession) resolveAndStartSeek(
	ctx context.Context,
	seekTime time.Time,
) (resolvedFrom time.Time, mode string, gap bool, err error) {
	resolvedFrom, mode, rerr := s.handler.svc.ResolvePlaybackStart(
		ctx, s.cameraID, seekTime, time.Time{},
	)
	if rerr != nil {
		if seekTime.After(time.Now()) {
			mode = PlaybackModeLive
		} else {
			return time.Time{}, "", false, rerr
		}
	}

	switch mode {
	case PlaybackModeLive:
		if err := s.startLive(ctx); err != nil {
			return time.Time{}, "", false, err
		}
	case PlaybackModeFirstAvailable:
		gap = true

		if err := s.startRecordedAt(ctx, resolvedFrom); err != nil {
			return time.Time{}, "", false, err
		}
	case PlaybackModeRecorded:
		if resolvedFrom.Sub(seekTime) >= gapThreshold {
			gap = true
		}

		if err := s.startRecordedAt(ctx, resolvedFrom); err != nil {
			return time.Time{}, "", false, err
		}
	}

	return resolvedFrom, mode, gap, nil
}

// sendSeekedResponse writes the seeked JSON response to the WebSocket.
func (s *streamSession) sendSeekedResponse(
	ctx context.Context,
	resolvedFrom time.Time,
	mode string,
	gap, codecChanged bool,
	newCodecStr string,
	seq int64,
) {
	actualTime := resolvedFrom
	if mode == PlaybackModeLive {
		actualTime = time.Now()
	}

	resp := seekedResponse{
		Type:         "seeked",
		WallClock:    actualTime.UTC().Format(av.RFC3339Milli),
		Mode:         mode,
		CodecChanged: codecChanged,
		Gap:          gap,
		Seq:          seq,
	}

	if codecChanged {
		resp.Codecs = newCodecStr
	}

	_ = wsjson.Write(ctx, s.wsConn, resp)
}

// startRecordedAt creates a recorded playback session starting at the given time.
// Unlike startResolved, this skips the resolution step (caller already resolved).
func (s *streamSession) startRecordedAt(ctx context.Context, from time.Time) error {
	factory := s.handler.svc.RecordedDemuxerFactory(s.cameraID, from, time.Time{})
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

// captureCodecStr returns the current MIME codec string from the MSE writer,
// or empty string if not yet available.
func (s *streamSession) captureCodecStr() string {
	s.mu.Lock()
	ms := s.ms
	s.mu.Unlock()

	if ms == nil {
		return ""
	}

	return ms.CodecString()
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

// parseWSCameraID extracts and validates the cameraId query parameter.
func parseWSCameraID(r *http.Request) (string, error) {
	id := r.URL.Query().Get("cameraId")
	if id == "" {
		return "", errCameraIDRequired
	}

	return id, nil
}

// parseWSOptionalTime reads an RFC3339 query parameter, returning zero if
// absent. Returns an error if present but not valid RFC3339.
func parseWSOptionalTime(r *http.Request, key string) (time.Time, error) {
	s := r.URL.Query().Get(key)
	if s == "" {
		return time.Time{}, nil
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, errInvalidStartRFC3339
	}

	return t, nil
}

// cancelWriter wraps an io.WriteCloser and cancels a context on Close.
// Used to enforce per-write timeouts on WebSocket frames.
type cancelWriter struct {
	io.WriteCloser

	cancel context.CancelFunc
}

func (cw *cancelWriter) Close() error {
	err := cw.WriteCloser.Close()
	cw.cancel()

	return err
}

// mustWriter returns the writer as-is, wrapping any error into a no-op writer
// so that the MSE layer sees a clean WriteCloser on every call.
func mustWriter(w io.WriteCloser, err error) io.WriteCloser {
	if err != nil {
		return &errWriteCloser{err: err}
	}

	return w
}

// errWriteCloser is an io.WriteCloser that always returns a stored error.
type errWriteCloser struct{ err error }

func (e *errWriteCloser) Write([]byte) (int, error) { return 0, e.err }
func (e *errWriteCloser) Close() error              { return nil }
