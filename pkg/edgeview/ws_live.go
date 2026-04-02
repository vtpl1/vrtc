package edgeview

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/format/mse"
)

var errCameraIDRequired = errors.New("camera_id is required")

// wsCommand is a JSON command from the WebSocket client.
type wsCommand struct {
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
	Time  string `json:"time,omitempty"` // RFC3339 timestamp for seek
}

// wsLive handles the /ws/live WebSocket endpoint.
//
// Protocol:
//   - Client sends JSON: {"type":"mse"} to start streaming
//   - Server sends text: {"type":"mse","value":"video/mp4; codecs=\"...\""}
//   - Server sends binary: fMP4 init segment, then continuous moof+mdat fragments
//   - Client may send {"type":"mse","value":"pause"} or {"type":"mse","value":"resume"}
//
//nolint:funlen,gocognit // WebSocket handler -- lifecycle complexity is inherent.
func (h *HTTPHandler) wsLive(w http.ResponseWriter, r *http.Request) {
	sourceID, err := parseWSCameraID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  []string{"*"},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		h.log.Error().Err(err).Msg("ws live: accept failed")

		return
	}

	defer func() {
		if err := wsConn.CloseNow(); err != nil {
			h.log.Error().Err(err).Msg("ws live: close")
		}
	}()

	ctx := r.Context()
	errWriteChan := make(chan error, 1)
	errReadChan := make(chan error, 1)

	rCtx, rCancel := context.WithCancel(ctx)
	defer rCancel()

	var (
		msMu sync.Mutex
		ms   *mse.MSEWriter

		consumerMu sync.Mutex
		consumer   av.ConsumerHandle
	)

	muxerOnce := &lazyOnce{}

	wg := sync.WaitGroup{}

	wg.Go(func() {
		defer close(errReadChan)

		for {
			select {
			case <-rCtx.Done():
				return
			default:
				cmd, rerr := readWSCommand(rCtx, wsConn)
				if rerr != nil {
					if !errors.Is(rerr, context.Canceled) &&
						!errors.Is(rerr, context.DeadlineExceeded) {
						errReadChan <- rerr
					}

					return
				}

				if cmd.Type == "mse" {
					if err := muxerOnce.Do(func() error {
						binaryWriterFactory := func() (io.WriteCloser, error) {
							return wsConn.Writer(ctx, websocket.MessageBinary)
						}
						textWriterFactory := func() (io.WriteCloser, error) {
							return wsConn.Writer(ctx, websocket.MessageText)
						}

						localMs, merr := mse.NewFromFactories(
							binaryWriterFactory,
							textWriterFactory,
						)
						if merr != nil {
							writeWSErrorResponse(ctx, wsConn, merr, "consume failed")

							return merr
						}

						msMu.Lock()
						ms = localMs
						msMu.Unlock()

						defer h.svc.TrackConsumer()()

						handle, cerr := h.svc.Hub().Consume(ctx, sourceID, av.ConsumeOptions{
							MuxerFactory: func(_ context.Context, _ string) (av.MuxCloser, error) {
								return localMs, nil
							},
							MuxerRemover: func(_ context.Context, _ string) error {
								return localMs.Close()
							},
							ErrChan: errWriteChan,
						})
						if cerr != nil {
							writeWSErrorResponse(ctx, wsConn, cerr, "consume failed")

							return cerr
						}

						consumerMu.Lock()
						consumer = handle
						consumerMu.Unlock()

						return nil
					}); err != nil {
						return
					}

					switch cmd.Value {
					case "": // initial subscription -- no action needed
					case "pause":
						if perr := h.svc.Hub().PauseRelay(ctx, sourceID); perr != nil {
							errReadChan <- perr

							return
						}
					case "resume":
						if rerr := h.svc.Hub().ResumeRelay(ctx, sourceID); rerr != nil {
							errReadChan <- rerr

							return
						}
					}
				}
			}
		}
	})

	select {
	case <-ctx.Done():
	case <-errWriteChan:
	case <-errReadChan:
	}

	rCancel()

	msMu.Lock()
	msCopy := ms
	msMu.Unlock()

	if msCopy != nil {
		_ = msCopy.Close()
	}

	consumerMu.Lock()
	consumerCopy := consumer
	consumerMu.Unlock()

	if consumerCopy != nil {
		if err := consumerCopy.Close(ctx); err != nil {
			h.log.Error().Err(err).Msg("ws live: consumer close")
		}
	}

	wg.Wait()
}

// lazyOnce executes a function at most once, recording any error.
type lazyOnce struct {
	once sync.Once
	err  error
}

func (l *lazyOnce) Do(f func() error) error {
	l.once.Do(func() {
		l.err = f()
	})

	return l.err
}

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
