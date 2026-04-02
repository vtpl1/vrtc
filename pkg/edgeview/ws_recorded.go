package edgeview

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/format/mse"
	"github.com/vtpl1/vrtc-sdk/av/relayhub"
)

// wsRecorded handles the /ws/recorded WebSocket endpoint.
//
// Protocol:
//   - Client connects with query params: sourceID, from (RFC3339), to (RFC3339, optional)
//   - Client sends JSON: {"type":"mse"} to start playback
//   - Server sends text: {"type":"mse","value":"video/mp4; codecs=\"...\""}
//   - Server sends binary: fMP4 init segment, then continuous moof+mdat fragments
//   - When "to" is empty, server enters follow mode and polls for new segments
//   - Client may send {"type":"mse","value":"pause"} or {"type":"mse","value":"resume"}
//
//nolint:funlen,gocognit // WebSocket handler -- lifecycle complexity is inherent.
func (h *HTTPHandler) wsRecorded(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("sourceID")
	if channelID == "" {
		channelID = r.URL.Query().Get("channel_id")
	}

	from, to, err := parseWSTimeRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	factory := h.svc.RecordedDemuxerFactory(channelID, from, to)

	playSM := relayhub.New(factory, nil)
	if err := playSM.Start(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	defer func() { _ = playSM.Stop() }()

	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  []string{"*"},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		h.log.Error().Err(err).Msg("ws recorded: accept failed")

		return
	}

	defer func() {
		if err := wsConn.CloseNow(); err != nil {
			h.log.Error().Err(err).Msg("ws recorded: close")
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
							writeWSErrorResponse(ctx, wsConn, merr)

							return merr
						}

						msMu.Lock()
						ms = localMs
						msMu.Unlock()

						handle, cerr := playSM.Consume(ctx, channelID, av.ConsumeOptions{
							MuxerFactory: func(_ context.Context, _ string) (av.MuxCloser, error) {
								return localMs, nil
							},
							MuxerRemover: func(_ context.Context, _ string) error {
								return localMs.Close()
							},
							ErrChan: errWriteChan,
						})
						if cerr != nil {
							writeWSErrorResponse(ctx, wsConn, cerr)

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
					case "": // initial play -- no action needed
					case "pause":
						if perr := playSM.PauseRelay(ctx, channelID); perr != nil {
							errReadChan <- perr

							return
						}
					case "resume":
						if rerr := playSM.ResumeRelay(ctx, channelID); rerr != nil {
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
			h.log.Error().Err(err).Msg("ws recorded: consumer close")
		}
	}

	wg.Wait()
}

// parseWSTimeRange extracts from/to RFC3339 query parameters for WebSocket playback.
func parseWSTimeRange(r *http.Request) (from, to time.Time, err error) {
	if s := r.URL.Query().Get("from"); s != "" {
		from, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return from, to, err
		}
	}

	if s := r.URL.Query().Get("to"); s != "" {
		to, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return from, to, err
		}
	}

	return from, to, nil
}
