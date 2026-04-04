# KPI Targets

Performance targets for the vrtc edge service. Measured via `cmd/loadtest/` and `/api/metrics`.

## Streaming & Latency

| KPI | Target | Metric | How to test |
|-----|--------|--------|-------------|
| Live view startup (TTFB) | < 500 ms | `live_view_startup_ms` | `loadtest stream --camera-id X` |
| RTSP session setup | < 300 ms | `rtsp_session_setup_ms` | Automatic (demuxer factory) |
| Glass-to-glass latency | < 1 s | — | Manual (visual) |
| Frame loss rate | < 0.1% | RelayStats.DroppedPackets | `loadtest stream` + `/api/cameras/stats` |
| Fragment continuity | 0 gaps | `fragment_gap_ms` | `/api/metrics` |

## Recording Reliability

| KPI | Target | Metric | How to test |
|-----|--------|--------|-------------|
| Recording uptime | >= 99.9% | Computed from segments | `loadtest recording --camera-id X` |
| Max recording gap | < 2 s | Gap analysis | `loadtest recording` |
| Segment rotation loss | 0 frames | — | Segment validation |
| Storage write throughput | 100% of bitrate | — | Monitor disk I/O |

## Recording Retrieval

| KPI | Target | Metric | How to test |
|-----|--------|--------|-------------|
| Playback startup | < 500 ms | `playback_startup_ms` | `loadtest playback --camera-id X` |
| Seek latency | < 300 ms | `seek_latency_ms` | WebSocket seek |
| Recorded-to-live transition | < 500 ms | `rec_to_live_transition_ms` | Follow-mode playback |
| Segment open time | < 50 ms | `segment_open_ms` | Automatic (chain demuxer) |
| Timeline query time | < 50 ms | `timeline_query_ms` | `loadtest timeline --camera-id X` |
| Concurrent playback sessions | >= 64 per camera | — | `loadtest playback -c 64` |
| Playback frame loss | 0% | — | Segment validation |
| Cross-segment continuity | 0 gaps | `fragment_gap_ms` | `/api/metrics` |

## Scalability & Stability

| KPI | Target | Metric | How to test |
|-----|--------|--------|-------------|
| Consumer burst tolerance | 20 concurrent adds | — | `loadtest burst --camera-id X` |
| Fan-out ratio | >= 20 consumers/relay | RelayStats.ConsumerCount | `loadtest stream -c 20` |
| CPU per stream | < 2% per 1080p | — | System monitoring |
| Memory per consumer | < 5 MB | — | System monitoring |
| Consumer disconnect isolation | 0 impact | — | `loadtest burst` |

## System Health

| KPI | Target | Metric | How to test |
|-----|--------|--------|-------------|
| API response time (p95) | < 100 ms | `api_response_ms` | `loadtest api` |
| Camera reconnection time | < 3 s | — | Kill/restart camera |
| Service restart recovery (MTTR) | < 30 s | — | Restart service |
| Graceful shutdown | < 10 s | — | Send SIGTERM |

## Endpoints

- **Metrics**: `GET /api/metrics?since=1h` — histograms, snapshots, per-relay KPIs
- **Stats**: `GET /api/cameras/stats/summary` — real-time system aggregates
- **Per-camera**: `GET /api/cameras/{id}/stats` — single camera metrics

## Loadtest Tool

```bash
# Build
make benchmark

# Live streaming
./bin/loadtest_linux_amd64 stream --camera-id cam45 -c 10 -d 30s

# Burst connect/disconnect
./bin/loadtest_linux_amd64 burst --camera-id cam45 -c 5 --cycles 50

# API latency
./bin/loadtest_linux_amd64 api -c 10 -d 10s

# Recording continuity
./bin/loadtest_linux_amd64 recording --camera-id cam45 --lookback 1h

# Playback
./bin/loadtest_linux_amd64 playback --camera-id cam45 -c 64 -d 30s

# Timeline query
./bin/loadtest_linux_amd64 timeline --camera-id cam45 -c 10 -d 10s
```
