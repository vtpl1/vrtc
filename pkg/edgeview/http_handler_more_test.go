package edgeview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc/pkg/metrics"
	"github.com/vtpl1/vrtc/pkg/pva"
	"github.com/vtpl1/vrtc/pkg/pva/persistence"
	"github.com/vtpl1/vrtc/pkg/recorder"
)

type edgeviewTestSegments struct{ active int }

func (s edgeviewTestSegments) ActiveCount() int { return s.active }

type edgeviewTestViewers struct{ count int }

func (v edgeviewTestViewers) ViewerCount() int { return v.count }

func newEdgeviewMetricsStore(t *testing.T) *metrics.Store {
	t.Helper()

	store, err := metrics.New(filepath.Join(t.TempDir(), "metrics.db"), 24*time.Hour, 1000)
	if err != nil {
		t.Fatalf("metrics.New: %v", err)
	}

	t.Cleanup(func() { _ = store.Close() })

	return store
}

func TestParseOptionalTimeRange_DefaultsLast24Hours(t *testing.T) {
	t.Parallel()

	start, end, err := parseOptionalTimeRange("", "")
	if err != nil {
		t.Fatalf("parseOptionalTimeRange: %v", err)
	}

	diff := end.Sub(start)
	if diff < 23*time.Hour || diff > 25*time.Hour {
		t.Fatalf("expected ~24 hour default window, got %s", diff)
	}
}

func TestHumaMetrics_UsesSinceQueryAndRelayMetrics(t *testing.T) {
	t.Parallel()

	store := newEdgeviewMetricsStore(t)
	hub := serviceTestRelayHub{
		statsByID: map[string]av.RelayStats{"cam-1": {
			ID:             "cam-1",
			PacketsRead:    100,
			DroppedPackets: 5,
			ActualFPS:      25,
			BitrateBps:     2048,
			ConsumerCount:  2,
			StartedAt:      time.Now().Add(-2 * time.Minute),
		}},
	}
	collector := metrics.NewCollector(store, hub, edgeviewTestSegments{active: 3}, edgeviewTestViewers{count: 4})
	defer collector.Stop()

	svc := NewService(log.Logger, serviceTestRelayHub{}, nil, nil)
	handler := NewHTTPHandler(svc, log.Logger, "", WithMetricsCollector(collector))

	store.RecordLatency(metrics.MetricTimelineQueryMs, 10*time.Millisecond, map[string]string{"camera_id": "cam-1"})
	time.Sleep(1100 * time.Millisecond)

	out, err := handler.humaMetrics(context.Background(), &metricsInput{Since: "30m"})
	if err != nil {
		t.Fatalf("humaMetrics: %v", err)
	}

	if len(out.Body.Relays) != 1 || out.Body.Relays[0].SourceID != "cam-1" {
		t.Fatalf("unexpected relay metrics: %+v", out.Body.Relays)
	}

	if out.Body.Uptime == "" {
		t.Fatal("expected non-empty uptime")
	}
}

func TestHumaSystemStats_AggregatesRelayAndViewerCounts(t *testing.T) {
	t.Parallel()

	hub := serviceTestRelayHub{
		statsByID: map[string]av.RelayStats{
			"cam-1": {
				ID:             "cam-1",
				PacketsRead:    100,
				BytesRead:      1000,
				KeyFrames:      4,
				DroppedPackets: 2,
				ActualFPS:      20,
				BitrateBps:     1000,
			},
			"cam-2": {
				ID:             "cam-2",
				PacketsRead:    200,
				BytesRead:      2000,
				KeyFrames:      8,
				DroppedPackets: 3,
				ActualFPS:      30,
				BitrateBps:     4000,
			},
		},
	}
	svc := NewService(log.Logger, hub, nil, nil, WithRecordingProvider(activeRecordingMap{
		"cam-1": true,
	}))
	svc.RegisterCamera(&CameraInfo{CameraID: "cam-1", Name: "Front"})
	svc.RegisterCamera(&CameraInfo{CameraID: "cam-2", Name: "Rear"})
	done1 := svc.TrackConsumer()
	done2 := svc.TrackConsumer()
	defer done1()
	defer done2()

	handler := NewHTTPHandler(svc, log.Logger, "", WithSegmentCounter(edgeviewTestSegments{active: 5}))

	out, err := handler.humaSystemStats(context.Background(), &struct{}{})
	if err != nil {
		t.Fatalf("humaSystemStats: %v", err)
	}

	if out.Body.TotalCameras != 2 || out.Body.StreamingCameras != 2 {
		t.Fatalf("unexpected camera counts: %+v", out.Body)
	}

	if out.Body.RecordingCameras != 1 || out.Body.ActiveSegments != 5 || out.Body.ActiveViewers != 2 {
		t.Fatalf("unexpected recording/viewer counts: %+v", out.Body)
	}

	if out.Body.TotalPacketsRead != 300 || out.Body.TotalDropped != 5 {
		t.Fatalf("unexpected packet totals: %+v", out.Body)
	}

	if out.Body.AvgFPS != 25 {
		t.Fatalf("expected avg FPS 25, got %v", out.Body.AvgFPS)
	}
}

func TestHumaCameraStatsByID_NotFound(t *testing.T) {
	t.Parallel()

	svc := NewService(log.Logger, serviceTestRelayHub{}, nil, nil)
	handler := NewHTTPHandler(svc, log.Logger, "")

	_, err := handler.humaCameraStatsByID(context.Background(), &cameraStatsInput{CameraID: "missing"})
	if err == nil {
		t.Fatal("expected not found error")
	}
}

func TestHumaRecordingTimeline_MapsFields(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	idx := &serviceTestIndex{
		entries: []recorder.RecordingEntry{{
			ID:         "seg-1",
			ChannelID:  "cam-1",
			StartTime:  base,
			EndTime:    base.Add(2 * time.Minute),
			SizeBytes:  2048,
			Status:     recorder.StatusComplete,
			HasMotion:  true,
			HasObjects: true,
		}},
	}
	svc := NewService(log.Logger, serviceTestRelayHub{}, idx, nil)
	handler := NewHTTPHandler(svc, log.Logger, "")

	out, err := handler.humaRecordingTimeline(context.Background(), &recordingTimelineInput{
		CameraID: "cam-1",
		Start:    base.Add(-1 * time.Minute).Format(time.RFC3339),
		End:      base.Add(3 * time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("humaRecordingTimeline: %v", err)
	}

	if len(out.Body.Items) != 1 {
		t.Fatalf("expected 1 recording entry, got %d", len(out.Body.Items))
	}

	if out.Body.Items[0].ID != "seg-1" || out.Body.Items[0].DurationMs != 120000 {
		t.Fatalf("unexpected recording timeline output: %+v", out.Body.Items[0])
	}
}

func TestExportOpenAPI_WritesJSONAndYAML(t *testing.T) {
	t.Parallel()

	store := newEdgeviewMetricsStore(t)
	dbm := persistence.NewDBManager(filepath.Join(t.TempDir(), "analytics"))
	t.Cleanup(func() { _ = dbm.Close() })

	svc := NewService(log.Logger, serviceTestRelayHub{}, nil, nil, WithChannelWriter(newFakeChannelWriter()))
	collector := metrics.NewCollector(store, serviceTestRelayHub{}, edgeviewTestSegments{}, edgeviewTestViewers{})
	defer collector.Stop()

	handler := NewHTTPHandler(
		svc,
		log.Logger,
		"",
		WithMetricsCollector(collector),
		WithAnalyticsHub(pva.NewAnalyticsHub()),
		WithAnalyticsReader(persistence.NewReader(dbm)),
	)

	outDir := filepath.Join(t.TempDir(), "openapi")
	if err := handler.ExportOpenAPI(outDir); err != nil {
		t.Fatalf("ExportOpenAPI: %v", err)
	}

	jsonSpec, err := os.ReadFile(filepath.Join(outDir, "openapi.json"))
	if err != nil {
		t.Fatalf("ReadFile(json): %v", err)
	}

	jsonBody := string(jsonSpec)
	for _, want := range []string{`"/api/cameras/ws/analytics"`, `"/api/cameras/import.csv"`, `"/api/metrics"`} {
		if !strings.Contains(jsonBody, want) {
			t.Fatalf("expected exported JSON spec to contain %s", want)
		}
	}

	yamlSpec, err := os.ReadFile(filepath.Join(outDir, "openapi.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(yaml): %v", err)
	}

	if !strings.Contains(string(yamlSpec), "/api/cameras/export.csv:") {
		t.Fatal("expected exported YAML spec to contain CSV export endpoint")
	}
}

func TestResolvePlaybackStart_NoRecordingsFound(t *testing.T) {
	t.Parallel()

	idx := &serviceTestIndex{}
	svc := NewService(log.Logger, serviceTestRelayHub{}, idx, nil)

	_, _, err := svc.ResolvePlaybackStart(context.Background(), "cam-1", time.Now().UTC().Add(-time.Hour), time.Time{})
	if !errorsIs(err, errNoRecordingsFound) {
		t.Fatalf("expected errNoRecordingsFound, got %v", err)
	}
}

type activeRecordingMap map[string]bool

func (m activeRecordingMap) ActiveChannels() map[string]bool { return m }
