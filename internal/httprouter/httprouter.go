package httprouter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/mse"
)

type Command struct {
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
}

func NewRouter(ctx context.Context, streamManager av.StreamManager) *chi.Mux {
	r := chi.NewRouter()
	// Middleware stack
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second)) // global timeout
	r.Use(logMiddleware)                        // your custom logger
	r.Use(cors.Handler(cors.Options{
		// AllowedOrigins:   []string{"https://foo.com"}, // Use this to allow specific origin hosts
		AllowedOrigins: []string{"https://*", "http://*"},
		// AllowOriginFunc:  func(r *http.Request, origin string) bool { return true },
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	}))
	// WebSocket endpoint
	r.Get("/v3/api/ws", func(w http.ResponseWriter, r *http.Request) {
		WSHandler(ctx, w, r, streamManager)
	})

	return r
}

type Lazy struct {
	once sync.Once
	err  error
}

func (l *Lazy) Do(f func() error) error {
	l.once.Do(func() {
		l.err = f()
	})

	return l.err
}

//nolint:gocognit // WebSocket handler — lifecycle complexity is inherent, not incidental.
func WSHandler(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	streamManager av.StreamManager,
) {
	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  []string{"*"}, // Allow all origins
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		log.Error().Err(err).Msg("WebSocket Accept failed.")

		return
	}

	defer func() {
		if err := wsConn.CloseNow(); err != nil {
			log.Error().Err(err).Msg("Websocket closed")
		} else {
			log.Info().Msg("Websocket closed")
		}
	}()

	producerID := r.URL.Query().Get("producerID")
	consumerID := r.URL.Query().Get("consumerID")
	log.Info().
		Str("producerID", producerID).
		Str("consumerID", consumerID).
		Msg("ws handler received")

	errWriteChan := make(chan error, 1)
	errReadChan := make(chan error, 1)

	// rCtx is cancelled when the write side errors or the connection closes,
	// unblocking the reader goroutine so wg.Wait() can return promptly.
	rCtx, rCancel := context.WithCancel(ctx)
	defer rCancel()

	var (
		msMu sync.Mutex
		ms   *mse.MSEWriter

		consumerMu sync.Mutex
		consumer   av.ConsumerHandle
	)

	wg := sync.WaitGroup{}
	muxerOnce := Lazy{}

	wg.Go(func() {
		defer close(errReadChan)

		for {
			select {
			case <-rCtx.Done():
				return
			default:
				cmd, err := ReadCommand(rCtx, wsConn)
				if err != nil {
					if !errors.Is(err, context.Canceled) &&
						!errors.Is(err, context.DeadlineExceeded) {
						errReadChan <- err

						log.Error().Err(err).Msg("Client disconnected or read failed.")
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
							WriteErrorResponse(ctx, wsConn, merr, "Consume failed")

							return merr
						}

						msMu.Lock()
						ms = localMs
						msMu.Unlock()

						handle, err := streamManager.Consume(ctx, producerID, av.ConsumeOptions{
							ConsumerID: consumerID,
							MuxerFactory: func(_ context.Context, _ string) (av.MuxCloser, error) {
								return localMs, nil
							},
							MuxerRemover: func(_ context.Context, _ string) error {
								localMs.Close() //nolint:contextcheck

								return nil
							},
							ErrChan: errWriteChan,
						})
						if err != nil {
							WriteErrorResponse(ctx, wsConn, err, "Consume failed")

							return err
						}

						consumerMu.Lock()
						consumer = handle
						consumerMu.Unlock()

						return nil
					}); err != nil {
						return
					}

					switch cmd.Value {
					case "": // initial subscription — no action needed
					case "pause":
						if err := streamManager.PauseProducer(ctx, producerID); err != nil {
							errReadChan <- err

							return
						}
					case "resume":
						if err := streamManager.ResumeProducer(ctx, producerID); err != nil {
							errReadChan <- err

							return
						}
					default:
						if cmd.Type != "heartBit" {
							log.Warn().
								Str("command type", cmd.Type).
								Str("command value", cmd.Value).
								Msg("Command channel blocked.")
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

	rCancel() // unblock the reader goroutine so wg.Wait() returns promptly

	msMu.Lock()
	msCopy := ms
	msMu.Unlock()

	if msCopy != nil {
		msCopy.Close() //nolint:contextcheck
	}

	consumerMu.Lock()
	consumerCopy := consumer
	consumerMu.Unlock()

	if consumerCopy != nil {
		if err := consumerCopy.Close(ctx); err != nil {
			log.Error().Err(err).Msg("ConsumerHandle.Close")
		}
	}

	wg.Wait()
}

func ReadCommand(ctx context.Context, wsConn *websocket.Conn) (Command, error) {
	cmd := Command{}
	err := wsjson.Read(ctx, wsConn, &cmd)

	return cmd, err
}

func WriteErrorResponse(ctx context.Context, wsConn *websocket.Conn, err error, msg string) {
	log.Error().Err(err).Msg(msg)
	errResponse := map[string]string{
		"type":  "error",
		"error": err.Error(),
	}

	WriteResponse(ctx, wsConn, errResponse)
}

func WriteResponse(ctx context.Context, wsConn *websocket.Conn, errResponse map[string]string) {
	_ = wsjson.Write(ctx, wsConn, errResponse)
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log := log.With().Str("method", r.Method).
			Str("url", r.URL.String()).
			Str("remote", r.RemoteAddr).Logger()

		start := time.Now()

		log.Info().Msg("Request received")

		next.ServeHTTP(w, r)

		log.Info().Dur("duration", time.Since(start)).Msg("Request served")
	})
}
