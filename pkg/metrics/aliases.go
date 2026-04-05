// Package metrics provides application-level instrumentation (Collector,
// middleware, metric names) on top of the generic metrics Store from the SDK.
//
// Core types (Store, Snapshot, Histogram, etc.) are defined in
// github.com/vtpl1/vrtc-sdk/metrics and re-exported here.
package metrics

import (
	"time"

	sdkmetrics "github.com/vtpl1/vrtc-sdk/metrics"
)

// Store is the central metrics collector backed by SQLite.
type Store = sdkmetrics.Store

// MetricsResponse is the JSON payload for GET /api/metrics.
type MetricsResponse = sdkmetrics.MetricsResponse

// Histogram holds pre-computed percentiles for a latency metric.
type Histogram = sdkmetrics.Histogram

// Snapshot is a periodic system-level reading.
type Snapshot = sdkmetrics.Snapshot

// RelayMetrics holds derived KPIs per relay.
type RelayMetrics = sdkmetrics.RelayMetrics

// QueryOpts controls what metrics are returned.
type QueryOpts = sdkmetrics.QueryOpts

// New opens or creates the SQLite DB at dbPath and starts the background writer.
func New(dbPath string, retention time.Duration, maxRows int64) (*Store, error) {
	return sdkmetrics.New(dbPath, retention, maxRows)
}
