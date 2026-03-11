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
	centralservicefrs "github.com/vtpl1/vrtc/gen/central_service_frs"
	streamservicefrs "github.com/vtpl1/vrtc/gen/stream_service_frs"
	"github.com/vtpl1/vrtc/internal/httprouter"
	"github.com/vtpl1/vrtc/pkg/configpath"
	"github.com/vtpl1/vrtc/pkg/lifecycle"
	"github.com/vtpl1/vrtc/pkg/logger"
	"github.com/vtpl1/vrtc/pkg/services/centralservicefrsimpl"
	"github.com/vtpl1/vrtc/pkg/services/streamservicefrsimpl"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func Run(appName, appMode string, cfg Config) error {
	logfile := configpath.GetLogFilePath(appName + "_" + appMode)

	closeLogger, err := logger.InitLogger(logfile, "info")
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}

	defer closeLogger()

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
	grpcL := s.MatchWithWriters(
		cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc"),
	)
	httpL := s.Match(cmux.HTTP1Fast())

	centralServer, err := centralservicefrsimpl.New()
	if err != nil {
		return err
	}
	defer centralServer.Close()

	streamServer, err := streamservicefrsimpl.New()
	if err != nil {
		return err
	}
	defer streamServer.Close()

	wg := sync.WaitGroup{}

	httpServer := &http.Server{
		Handler:           httprouter.NewRouter(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	grpcServer := grpc.NewServer()
	centralservicefrs.RegisterCentralServiceServer(grpcServer, centralServer)
	streamservicefrs.RegisterStreamServiceServer(grpcServer, streamServer)
	reflection.Register(grpcServer)

	wg.Go(func() {
		log.Info().Int("port", port).Msgf("[grpc-server] started")

		if err := grpcServer.Serve(grpcL); err != nil {
			if errors.Is(err, cmux.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
				return
			}

			log.Error().Err(err).Msg("[grpc-server] error")
		}
	})
	wg.Go(func() {
		log.Info().Int("port", port).Msgf("[http-server] started")

		if err := httpServer.Serve(httpL); err != nil {
			if errors.Is(err, cmux.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
				return
			}

			log.Error().Err(err).Msg("[http-server] error")
		}
	})
	wg.Go(func() {
		log.Info().Int("port", port).Msgf("[cmux-server] started")

		if err := s.Serve(); err != nil {
			if errors.Is(err, cmux.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
				return
			}

			log.Error().Err(err).Msg("[cmux-server] error")
		}
	})

	fmt.Println("waiting for termination request") //nolint:forbidigo
	lifecycle.WaitForTerminationRequest(errChan)
	fmt.Println("\nafter termination request") //nolint:forbidigo
	cancel()
	s.Close()
	fmt.Println("\nafter cmux close") //nolint:forbidigo
	grpcServer.GracefulStop()
	fmt.Println("\nafter grpc close") //nolint:forbidigo

	detachedCtx := context.WithoutCancel(ctx)

	shutCtx, cancel := context.WithTimeout(detachedCtx, 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutCtx); err != nil {
		log.Error().Err(err).Msg("HTTP shutdown error")
	}

	fmt.Println("\nafter http close") //nolint:forbidigo
	wg.Wait()
	log.Info().Msg("Server shut down gracefully")
	fmt.Println("Server shut down gracefully") //nolint:forbidigo

	return nil
}
