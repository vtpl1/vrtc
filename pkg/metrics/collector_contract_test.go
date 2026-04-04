package metrics_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc/pkg/metrics"
)

type metricsTestRelayHub struct {
	stats []av.RelayStats
}

func (h metricsTestRelayHub) GetRelayStats(context.Context) []av.RelayStats { return h.stats }

func (h metricsTestRelayHub) GetRelayStatsByID(_ context.Context, sourceID string) (av.RelayStats, bool) {
	for _, stat := range h.stats {
		if stat.ID == sourceID {
			return stat, true
		}
	}

	return av.RelayStats{}, false
}

func (h metricsTestRelayHub) ListRelayIDs(context.Context) []string { return nil }

func (h metricsTestRelayHub) GetActiveRelayCount(context.Context) int { return len(h.stats) }

func (h metricsTestRelayHub) Consume(context.Context, string, av.ConsumeOptions) (av.ConsumerHandle, error) {
	return nil, nil
}

func (h metricsTestRelayHub) PauseRelay(context.Context, string) error { return nil }

func (h metricsTestRelayHub) ResumeRelay(context.Context, string) error { return nil }

func (h metricsTestRelayHub) Start(context.Context) error { return nil }

func (h metricsTestRelayHub) SignalStop() bool { return true }

func (h metricsTestRelayHub) WaitStop() error { return nil }

func (h metricsTestRelayHub) Stop() error { return nil }

type metricsTestSegments struct{ active int }

func (c metricsTestSegments) ActiveCount() int { return c.active }

type metricsTestViewers struct{ active int }

func (c metricsTestViewers) ViewerCount() int { return c.active }

func newCollectorTestStore(t *testing.T) *metrics.Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "metrics.db")
	store, err := metrics.New(dbPath, 24*time.Hour, 10000)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	return store
}

func TestCollectorRecordSeekLatency_PersistsMetric(t *testing.T) {
	t.Parallel()

	store := newCollectorTestStore(t)
	collector := metrics.NewCollector(store, metricsTestRelayHub{}, nil, nil)
	t.Cleanup(collector.Stop)

	collector.RecordSeekLatency(125*time.Millisecond, "cam-1")
	time.Sleep(2 * time.Second)

	resp, err := store.Query(context.Background(), metrics.QueryOpts{Since: time.Hour})
	if err != nil {
		t.Fatal(err)
	}

	h, ok := resp.Latencies[metrics.MetricSeekLatencyMs]
	if !ok {
		t.Fatalf("expected %q metric to be present", metrics.MetricSeekLatencyMs)
	}

	if h.Count != 1 {
		t.Fatalf("expected 1 seek latency sample, got %d", h.Count)
	}
}

func TestCollectorRelayMetrics_ComputesLossRateAndCounts(t *testing.T) {
	t.Parallel()

	hub := metricsTestRelayHub{
		stats: []av.RelayStats{
			{
				ID:             "cam-1",
				PacketsRead:    1000,
				DroppedPackets: 5,
				ConsumerCount:  3,
				ActualFPS:      25,
				BitrateBps:     2_000_000,
				StartedAt:      time.Now().Add(-10 * time.Second),
			},
		},
	}
	store := newCollectorTestStore(t)
	collector := metrics.NewCollector(store, hub, metricsTestSegments{active: 2}, metricsTestViewers{active: 4})
	t.Cleanup(collector.Stop)

	got := collector.RelayMetrics(context.Background())
	if len(got) != 1 {
		t.Fatalf("expected 1 relay metric, got %d", len(got))
	}

	if got[0].FrameLossRate != 0.005 {
		t.Fatalf("expected frame loss rate 0.005, got %v", got[0].FrameLossRate)
	}

	if got[0].ConsumerCount != 3 {
		t.Fatalf("expected consumer count 3, got %d", got[0].ConsumerCount)
	}
}

func TestChiMiddleware_RecordsAPIResponseMetric(t *testing.T) {
	t.Parallel()

	store := newCollectorTestStore(t)
	collector := metrics.NewCollector(store, metricsTestRelayHub{}, nil, nil)
	t.Cleanup(collector.Stop)

	handler := metrics.ChiMiddleware(collector)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	time.Sleep(2 * time.Second)

	resp, err := store.Query(context.Background(), metrics.QueryOpts{Since: time.Hour})
	if err != nil {
		t.Fatal(err)
	}

	h, ok := resp.Latencies[metrics.MetricAPIResponseMs]
	if !ok {
		t.Fatalf("expected %q metric to be present", metrics.MetricAPIResponseMs)
	}

	if h.Count != 1 {
		t.Fatalf("expected 1 api response sample, got %d", h.Count)
	}
}
