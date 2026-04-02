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

var errNoPlaybackSession = errors.New("no playback session")

// wsRecorded handles the /ws/recorded WebSocket endpoint.
//
// Protocol:
//   - Client connects with query params: camera_id, start (RFC3339), end (RFC3339, optional)
//   - Client sends JSON: {"type":"mse"} to start playback
//   - Server sends text: {"type":"mse","value":"video/mp4; codecs=\"...\""}
//   - Server sends binary: fMP4 init segment, then continuous moof+mdat fragments
//   - When "end" is empty, server enters follow mode and polls for new segments
//   - Client may send {"type":"mse","value":"pause"} or {"type":"mse","value":"resume"}
//   - Client may send {"type":"mse","value":"seek","time":"2026-04-02T13:15:00Z"} to seek
//
//nolint:funlen // WebSocket handler -- lifecycle complexity is inherent.
func (h *HTTPHandler) wsRecorded(w http.ResponseWriter, r *http.Request) {
	cameraID, err := parseWSCameraID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	start, end, err := parseWSTimeRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  []string{"*"},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		h.log.Error().Err(err).Msg("ws recorded: accept failed")

		return
	}

	defer func() { _ = wsConn.CloseNow() }()

	ctx := r.Context()

	// Playback session — can be replaced on seek.
	session := &recordedSession{
		handler:  h,
		wsConn:   wsConn,
		cameraID: cameraID,
		end:      end,
	}

	if err := session.start(ctx, start); err != nil {
		writeWSErrorResponse(ctx, wsConn, err, "playback start failed")

		return
	}

	defer session.stop(ctx)

	errChan := make(chan error, 1)

	rCtx, rCancel := context.WithCancel(ctx)
	defer rCancel()

	// Read loop — handles mse commands including seek.
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

// recordedSession manages a single playback pipeline (relay hub + consumer).
// On seek, the session is stopped and restarted with a new start time.
type recordedSession struct {
	handler  *HTTPHandler
	wsConn   *websocket.Conn
	cameraID string
	end      time.Time

	mu       sync.Mutex
	playSM   *relayhub.RelayHub
	ms       *mse.MSEWriter
	consumer av.ConsumerHandle
}

func (s *recordedSession) start(ctx context.Context, from time.Time) error {
	factory := s.handler.svc.RecordedDemuxerFactory(s.cameraID, from, s.end)
	sm := relayhub.New(factory, nil)

	if err := sm.Start(ctx); err != nil {
		return fmt.Errorf("start playback: %w", err)
	}

	s.mu.Lock()
	s.playSM = sm
	s.ms = nil
	s.consumer = nil
	s.mu.Unlock()

	return nil
}

func (s *recordedSession) attachConsumer(ctx context.Context, errChan chan<- error) error {
	s.mu.Lock()
	sm := s.playSM
	s.mu.Unlock()

	if sm == nil {
		return errNoPlaybackSession
	}

	binaryFactory := func() (io.WriteCloser, error) {
		return s.wsConn.Writer(ctx, websocket.MessageBinary)
	}
	textFactory := func() (io.WriteCloser, error) {
		return s.wsConn.Writer(ctx, websocket.MessageText)
	}

	localMs, err := mse.NewFromFactories(binaryFactory, textFactory)
	if err != nil {
		return err
	}

	handle, cerr := sm.Consume(ctx, s.cameraID, av.ConsumeOptions{
		MuxerFactory: func(_ context.Context, _ string) (av.MuxCloser, error) {
			return localMs, nil
		},
		MuxerRemover: func(_ context.Context, _ string) error {
			return localMs.Close()
		},
		ErrChan: errChan,
	})
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

func (s *recordedSession) pause(ctx context.Context) {
	s.mu.Lock()
	sm := s.playSM
	s.mu.Unlock()

	if sm != nil {
		_ = sm.PauseRelay(ctx, s.cameraID)
	}
}

func (s *recordedSession) resume(ctx context.Context) {
	s.mu.Lock()
	sm := s.playSM
	s.mu.Unlock()

	if sm != nil {
		_ = sm.ResumeRelay(ctx, s.cameraID)
	}
}

func (s *recordedSession) stop(ctx context.Context) {
	s.mu.Lock()
	consumer := s.consumer
	ms := s.ms
	sm := s.playSM
	s.consumer = nil
	s.ms = nil
	s.playSM = nil
	s.mu.Unlock()

	if consumer != nil {
		_ = consumer.Close(ctx)
	}

	if ms != nil {
		_ = ms.Close()
	}

	if sm != nil {
		_ = sm.Stop()
	}
}

// parseWSTimeRange extracts start/end RFC3339 query parameters for WebSocket playback.
func parseWSTimeRange(r *http.Request) (start, end time.Time, err error) {
	if s := r.URL.Query().Get("start"); s != "" {
		start, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return start, end, errInvalidStartRFC3339
		}
	}

	if s := r.URL.Query().Get("end"); s != "" {
		end, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return start, end, errInvalidEndRFC3339
		}
	}

	if !end.IsZero() && end.Before(start) {
		return start, end, errInvalidTimeRange
	}

	return start, end, nil
}
