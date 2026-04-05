# Streaming Architecture

## Dual-Hub Design

The edge service runs two independent relay hubs that share the same camera RTSP connection:

```
Camera (RTSP)
    |
avgrabber.Demuxer (raw H.264/H.265 + AAC)
    |
+====================+
| LIVE HUB           |  real-time, zero delay, no analytics
| (RelayHub #1)      |  pktBuf: 30-second sliding window
+====================+
    |                \_______________
    |                                \
    | fan-out to consumers            | ProxyMuxDemuxer
    |                                 | reads near-live older frames from
    v                                 | recording infrastructure (disk + pktBuf)
 [Recorder]  [MSE live]  [HTTP]      |
                                      v
                                [BlockingMerger]
                                  waits per video packet until
                                  analytics arrive (up to maxWait)
                                      |
                                +====================+
                                | ANALYTICS HUB      |  delayed (~5s), enriched
                                | (RelayHub #2)      |  pktBuf: 30-second window
                                +====================+
                                      |
                                      v
                                 [MSE analytics viewers]
```

### Live Hub (RelayHub #1)

- Source: raw `avgrabber.Demuxer` (no analytics injection)
- Packets have `Analytics = nil` on the live path
- Serves real-time, low-latency consumers (live view, recorder, HTTP stream)
- Maintains a 30-second packet ring buffer per relay for GOP replay and recorded-to-live transition
- Delivery: 1 consumer = blocking (back-pressure); 2+ = leaky (slow consumers drop frames)

### Analytics Hub (RelayHub #2)

- Source: `ProxyMuxDemuxer` wrapped by `BlockingMerger`
- The proxy reads near-real-time older frames via the recording infrastructure:
  - **Primary path**: `RecordedDemuxerFactory` chains disk fMP4 segments, then transitions to the live packet buffer when segments are exhausted
  - **Fallback**: when no recordings exist, reads directly from the live hub's packet buffer
- `BlockingMerger` blocks on each video packet (up to `maxWait`) until analytics arrive in the `AnalyticsStore`, using `AnalyticsHub` pub/sub notifications to avoid polling
- Audio packets pass through without blocking
- If analytics don't arrive within `maxWait`, the packet passes through without them

### Configuration

| Field | Default | Description |
|-------|---------|-------------|
| `analytics_delay` | `5s` | How far behind live the analytics hub reads |
| `analytics_max_wait` | `7s` | Max time to block per video packet waiting for analytics |

The only timing source of truth is the avgrabber wall-clock (`Packet.WallClockTime`). The analytics tool echoes this same wall-clock back via the gRPC `IngestAnalytics` `wall_clock_ms` field, so no clock-skew compensation is needed.

---

## Streaming Modes

### 1. Live (raw)

No analytics. Real-time, lowest latency.

| Transport | Endpoint | Params |
|-----------|----------|--------|
| WebSocket MSE | `/api/cameras/ws/stream` | `cameraId=<id>` (no `start`) |
| HTTP chunked fMP4 | `/api/cameras/{cameraId}/stream` | (no `start`) |

- Consumer attaches to the **live hub**
- Multi-consumer fan-out (leaky delivery when 2+)
- GOP replay from 30-second buffer on attach (instant keyframe)

### 2. Live (analytics-enriched)

Delayed by ~`analytics_delay`. Each video frame carries `FrameAnalytics` when available.

| Transport | Endpoint | Params |
|-----------|----------|--------|
| WebSocket MSE | `/api/cameras/ws/stream` | `cameraId=<id>`, `analytics=true` |

- Consumer attaches to the **analytics hub**
- Packets arrive ~5 seconds behind real-time
- `pkt.Analytics` contains detections, counts, bounding boxes
- If analytics processing is slow or unavailable, frames pass through without analytics after `maxWait`

### 3. Analytics-only (JSON, no video)

Pure analytics JSON stream, no video data.

| Transport | Endpoint | Params |
|-----------|----------|--------|
| WebSocket text | `/api/cameras/ws/analytics` | `cameraId=<id>` |

- Subscribes to the `AnalyticsHub` pub/sub
- Receives `FrameAnalytics` JSON text frames as analytics arrive
- No video, no fMP4 -- just detection data
- Non-blocking: slow consumers drop frames

### 4. Recorded playback

Plays back from fMP4 segments on disk.

| Transport | Endpoint | Params |
|-----------|----------|--------|
| WebSocket MSE | `/api/cameras/ws/stream` | `cameraId=<id>`, `start=<RFC3339>` |
| HTTP chunked fMP4 | `/api/cameras/{cameraId}/stream` | `start=<RFC3339>` |

- Creates a **per-session playback hub** (`WithMaxConsumers(1)` -- single consumer, blocking delivery, no frame drops)
- `ChainingDemuxer` chains disk fMP4 segments with monotonic DTS adjustment
- Supports gap detection (sets `IsDiscontinuity` when wall-clock gaps > 5s between segments)
- In follow mode (`to` is zero), transitions seamlessly to the live packet buffer when disk segments are exhausted

### 5. Seek (mode switching)

WebSocket clients can seek between live and recorded transparently.

| Command | Behavior |
|---------|----------|
| `{"type":"mse","value":"seek","time":"<RFC3339>"}` | Seek to absolute time |
| `{"type":"mse","value":"skip","offset":"-30s"}` | Relative seek from current position |

- Seeking to the past: switches to recorded mode (per-session hub)
- Seeking to the future / beyond latest recording: switches to live mode
- Response: `{"type":"seeked","wallClock":"...","mode":"...","codecChanged":...}`
- Codec changes at seek boundaries are detected and propagated

### 6. Pause / Resume

| Command | Behavior |
|---------|----------|
| `{"type":"mse","value":"pause"}` | Pauses delivery |
| `{"type":"mse","value":"resume"}` | Resumes delivery |

- **Recorded mode**: pauses the per-session relay's demuxer (if it implements `av.Pauser`)
- **Live mode**: no-op on the shared relay (pausing would stop all consumers including the recorder). The client-side player handles buffering.

---

## Data Flow: Analytics Injection

```
Analytics Tool (external)
    |
    | gRPC: AnalyticsIngestionService.IngestAnalytics
    v
AnalyticsPipeline.Handle(sourceID, wallClock, FrameAnalytics)
    |
    +---> AnalyticsStore.Put(sourceID, wallClock, analytics)
    |         time-indexed, 30s TTL, binary-search lookup
    |
    +---> AnalyticsHub.Broadcast(sourceID, analytics)
              pub/sub to all subscribers (BlockingMerger, /ws/analytics)
```

When the analytics hub's relay reads a video packet:

1. `BlockingMerger.ReadPacket()` reads from proxy (delayed packet)
2. Fast path: `AnalyticsStore.lookup()` -- analytics already in store? Inject and return.
3. Slow path: subscribe to `AnalyticsHub`, wait for notification, re-check store on each notification
4. Timeout: return packet without analytics after `maxWait`

---

## Key Components

| Component | File | Purpose |
|-----------|------|---------|
| `BlockingMerger` | `pkg/pva/blocking_merger.go` | Blocks per packet until analytics arrive |
| `ProxyMuxDemuxer` | `pkg/pva/proxy.go` | Bridges recording infra to analytics hub |
| `NewAnalyticsDemuxerFactory` | `pkg/pva/proxy.go` | DemuxerFactory for the analytics hub |
| `MetadataMerger` | `pkg/pva/merger.go` | Immediate (non-blocking) analytics injection (unused on live path) |
| `AnalyticsStore` | `pkg/pva/store.go` | Time-indexed analytics storage with 200ms match tolerance |
| `AnalyticsHub` | `pkg/pva/store.go` | Per-sourceID pub/sub broadcaster |
| `AnalyticsPipeline` | `pkg/pva/store.go` | Wires store + hub as gRPC handler |
| `RelayHub` | `vrtc-sdk/av/relayhub/` | Fan-out coordinator (one relay per source, N consumers) |
| `packetbuf.Buffer` | `vrtc-sdk/av/packetbuf/` | 30-second ring buffer with `Demuxer(since)` replay |
| `ChainingDemuxer` | `vrtc-sdk/av/chain/` | Chains segment demuxers with DTS adjustment |
| `RecordedDemuxerFactory` | `pkg/edgeview/stream.go` | Disk segments + live buffer transition |

## WebSocket Text Frame Protocol

| `type` | Key fields | When sent |
|--------|------------|-----------|
| `mode_change` | `mode`, `wallClock` | Live mode activation |
| `playback_info` | `mode`, `actualStartWallClock`, `wallClock` | Recorded/first_available start |
| `seeked` | `wallClock`, `mode`, `codecChanged`, `codecs?`, `gap?`, `seq` | After seek completes |
| `timing` | `wallClock` | Every fragment flush |
| `mse` | `value` (MIME codec string) | Codec negotiation |
| `error` | `error` | Invalid command |
| *(analytics)* | Full `FrameAnalytics` JSON | When analytics are attached to a packet |
