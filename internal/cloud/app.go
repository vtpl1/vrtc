package cloud

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/soheilhy/cmux"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/relayhub"
	"github.com/vtpl1/vrtc/pkg/edgeview"
	"github.com/vtpl1/vrtc/pkg/lifecycle"
)

// errNotImplemented is returned by the stub demuxer factory until the gRPC
// source layer is rewritten.
var errNotImplemented = errors.New("cloud demuxer: not implemented")

//nolint:funlen // server-lifecycle wiring cannot be split cleanly
func Run(appName, appMode string, cfg Config) error {
	log.Info().Msgf("%+v", cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lc := net.ListenConfig{}

	port := cfg.API.Listen

	listener, err := lc.Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Error().Err(err).Msgf("[tcp] failed to listen on [%v]", port)

		return err
	}

	errChan := make(chan error)

	s := cmux.New(listener)
	httpL := s.Match(cmux.Any())

	// Stub demuxer factory — replaced when gRPC source layer is rewritten.
	demuxerFactory := av.DemuxerFactory(
		func(_ context.Context, sourceID string) (av.DemuxCloser, error) {
			return nil, fmt.Errorf("%w: sourceID=%s", errNotImplemented, sourceID)
		},
	)

	demuxerRemover := av.DemuxerRemover(func(_ context.Context, _ string) error { return nil })

	sm := relayhub.New(demuxerFactory, demuxerRemover)

	if err := sm.Start(ctx); err != nil {
		log.Error().Err(err).Msg("stream manager start error")

		return err
	}

	viewSvc := edgeview.NewService(log.Logger, sm, nil, nil)
	viewHandler := edgeview.NewHTTPHandler(viewSvc, log.Logger, "")

	httpServer := &http.Server{
		Handler:           viewHandler.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	wg := sync.WaitGroup{}

	wg.Go(func() {
		log.Info().Int("port", port).Msg("[http-server] started")

		if err := httpServer.Serve(httpL); err != nil {
			if errors.Is(err, cmux.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
				return
			}

			log.Error().Err(err).Msg("[http-server] error")
		}
	})

	wg.Go(func() {
		log.Info().Int("port", port).Msg("[cmux-server] started")

		if err := s.Serve(); err != nil {
			if errors.Is(err, cmux.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
				return
			}

			log.Error().Err(err).Msg("[cmux-server] error")
		}
	})

	log.Info().
		Str("appName", appName).
		Str("appMode", appMode).
		Int("port", port).
		Msg("cloud node starting")

	lifecycle.WaitForTerminationRequest(errChan)

	if err := sm.Stop(); err != nil {
		log.Error().Err(err).Msg("stream manager stop error")
	}

	cancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()

	if err := httpServer.Shutdown(shutCtx); err != nil {
		log.Error().Err(err).Msg("http shutdown error")
	}

	s.Close()
	wg.Wait()

	log.Info().Msg("cloud node shut down gracefully")

	return nil
}
