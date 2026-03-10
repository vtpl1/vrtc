// Command frsbridge bridges an FRS gRPC stream to a WebSocket MSE server.
//
// The FRS service streams FramePVA frames over gRPC (client-streaming); this
// tool demuxes them with pkg/frs and remuxes to fMP4 over WebSocket using
// pkg/mse, wired together via pkg/av/streammanager3.
//
// Usage:
//
//	frsbridge [flags]
//
// Flags:
//
//	--grpc-addr    gRPC listen address  (default ":50051")
//	--ws-addr      WebSocket listen address (default ":8080")
//	--producer-id  stream manager producer ID (default "frs")
//	--consumer-id  stream manager consumer ID (default "mse")
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/streammanager3"
	"github.com/vtpl1/vrtc/pkg/frs"
	"github.com/vtpl1/vrtc/pkg/mse"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	var (
		grpcAddr   string
		wsAddr     string
		producerID string
		consumerID string
	)

	root := &cobra.Command{
		Use:   "frsbridge",
		Short: "Bridge FRS gRPC stream to WebSocket MSE",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), grpcAddr, wsAddr, producerID, consumerID)
		},
	}

	root.Flags().StringVar(&grpcAddr, "grpc-addr", ":50051", "gRPC listen address")
	root.Flags().StringVar(&wsAddr, "ws-addr", ":8080", "WebSocket MSE listen address")
	root.Flags().StringVar(&producerID, "producer-id", "frs", "stream manager producer ID")
	root.Flags().StringVar(&consumerID, "consumer-id", "mse", "stream manager consumer ID")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := root.ExecuteContext(ctx); err != nil {
		log.Error().Err(err).Msg("command failed")
		os.Exit(1)
	}
}

func run(ctx context.Context, grpcAddr, wsAddr, producerID, consumerID string) error {
	// ── 1. Start the FRS gRPC server (av.DemuxCloser) ────────────────────────
	frsServer, err := frs.New(grpcAddr)
	if err != nil {
		return err
	}
	defer frsServer.Close()

	log.Info().Str("addr", grpcAddr).Msg("FRS gRPC server listening")

	// ── 2. Start the MSE WebSocket server (av.MuxCloser) ─────────────────────
	mseServer, err := mse.New(wsAddr)
	if err != nil {
		return err
	}
	defer mseServer.Close()

	log.Info().Str("addr", wsAddr).Msg("MSE WebSocket server listening")

	// ── 3. Wire them through the stream manager ───────────────────────────────
	demuxFactory := av.DemuxerFactory(func(_ context.Context, _ string) (av.DemuxCloser, error) {
		return frsServer, nil
	})

	muxFactory := av.MuxerFactory(func(_ context.Context, _ string) (av.MuxCloser, error) {
		return mseServer, nil
	})

	sm := streammanager3.New(demuxFactory, nil)

	if err := sm.Start(ctx); err != nil {
		return err
	}

	errCh := make(chan error, 1)
	if err := sm.AddConsumer(ctx, producerID, consumerID, muxFactory, nil, errCh); err != nil {
		return err
	}

	log.Info().
		Str("producer", producerID).
		Str("consumer", consumerID).
		Msg("stream manager running — waiting for signal")

	// ── 4. Block until context is cancelled or the stream errors ─────────────
	select {
	case <-ctx.Done():
		log.Info().Msg("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			log.Error().Err(err).Msg("stream error")
		}
	}

	return sm.Stop()
}
