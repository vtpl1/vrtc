package edge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc/pkg/channel"
	"github.com/vtpl1/vrtc/pkg/edgeview"
	"github.com/vtpl1/vrtc/pkg/metrics"
	"github.com/vtpl1/vrtc-sdk/av/pva"
	"github.com/vtpl1/vrtc-sdk/av/pva/persistence"
)

var errOpenAPIRelayConsume = errors.New("openapi relay stub does not support consumption")

type openAPIRelayHub struct{}

func (openAPIRelayHub) GetRelayStats(context.Context) []av.RelayStats { return nil }

func (openAPIRelayHub) GetRelayStatsByID(context.Context, string) (av.RelayStats, bool) {
	return av.RelayStats{}, false
}

func (openAPIRelayHub) ListRelayIDs(context.Context) []string { return nil }

func (openAPIRelayHub) GetActiveRelayCount(context.Context) int { return 0 }

func (openAPIRelayHub) Consume(
	context.Context,
	string,
	av.ConsumeOptions,
) (av.ConsumerHandle, error) {
	return nil, errOpenAPIRelayConsume
}

func (openAPIRelayHub) PauseRelay(context.Context, string) error { return nil }

func (openAPIRelayHub) ResumeRelay(context.Context, string) error { return nil }

func (openAPIRelayHub) Start(context.Context) error { return nil }

func (openAPIRelayHub) SignalStop() bool { return true }

func (openAPIRelayHub) WaitStop() error { return nil }

func (openAPIRelayHub) Stop() error { return nil }

type openAPISegmentCounter struct{}

func (openAPISegmentCounter) ActiveCount() int { return 0 }

type openAPIChannelWriter struct{}

func (openAPIChannelWriter) GetChannel(context.Context, string) (channel.Channel, error) {
	return channel.Channel{}, channel.ErrChannelNotFound
}

func (openAPIChannelWriter) ListChannels(context.Context) ([]channel.Channel, error) {
	return nil, nil
}

func (openAPIChannelWriter) Close() error { return nil }

func (openAPIChannelWriter) SaveChannel(context.Context, channel.Channel) error { return nil }

func (openAPIChannelWriter) DeleteChannel(context.Context, string) error { return nil }

// ExportOpenAPI writes a full edge API contract for downstream UI generation.
func ExportOpenAPI(outputDir string) error {
	tempDir, err := os.MkdirTemp("", "vrtc-openapi-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	hub := openAPIRelayHub{}
	segmentCounter := openAPISegmentCounter{}

	metricsStore, err := metrics.New(filepath.Join(tempDir, "metrics.db"), time.Hour, 1000)
	if err != nil {
		return err
	}
	defer metricsStore.Close()

	analyticsDBM := persistence.NewDBManager(filepath.Join(tempDir, "analytics"))
	defer analyticsDBM.Close()

	svc := edgeview.NewService(
		log.Logger,
		hub,
		nil,
		nil,
		edgeview.WithChannelWriter(openAPIChannelWriter{}),
	)

	collector := metrics.NewCollector(metricsStore, hub, segmentCounter, svc)
	defer collector.Stop()

	handler := edgeview.NewHTTPHandler(
		svc,
		log.Logger,
		"",
		edgeview.WithSegmentCounter(segmentCounter),
		edgeview.WithMetricsCollector(collector),
		edgeview.WithAnalyticsHub(pva.NewAnalyticsHub()),
		edgeview.WithAnalyticsReader(persistence.NewReader(analyticsDBM)),
	)

	return handler.ExportOpenAPI(outputDir)
}
