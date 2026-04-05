# Live View Flow Audit: Analytics-Enriched vs Video/Audio Only

## Architecture: Dual-Hub Design

The system uses two separate `RelayHub` instances to cleanly separate real-time video from analytics-enriched streams:

| | Video/Audio Only (default) | Analytics-Enriched (`?analytics=true`) |
|---|---|---|
| **Hub** | Main `RelayHub` | `AnalyticsRelayHub` |
| **Latency** | ~0ms (real-time) | ~5s delay + up to 7s blocking/frame |
| **Source** | Raw `avgrabber.Demuxer` | `ProxyMuxDemuxer` -> `BlockingMerger` |
| **`pkt.Analytics`** | Always `nil` | Set when available, `nil` on timeout |
| **HTTP support** | Yes | No (WebSocket only) |
| **Delivery** | Multi-consumer leaky fan-out | Multi-consumer leaky fan-out |

---

## Live View Modes

### 1. Live (Raw) -- Real-time Video Only

No analytics, zero latency.

| Transport | Endpoint | Query Params |
|-----------|----------|--------------|
| WebSocket MSE | `/api/cameras/ws/stream` | `cameraId=<id>` (no `start`, no `analytics`) |
| HTTP chunked fMP4 | `/api/cameras/{cameraId}/stream` | (no `start`) |

**Flow:**

1. Client connects with `cameraId` only.
2. `streamSession.start()` -> calls `startLive()` (zero time = live mode).
3. Session attaches to **Main Hub** via `hub.Consume()`.
4. Server sends `{"type":"mode_change","mode":"live"}`.
5. Multi-consumer fan-out; slow consumers drop frames (leaky mode when 2+ consumers).

**File locations:**

- Handler: `pkg/edgeview/ws_stream.go:startLive()` (line 162)
- HTTP: `pkg/edgeview/http_handler.go:httpStreamLive()` (line 890)
- Service: `pkg/edgeview/stream.go:Hub()` (line 146)

### 2. Live (Analytics-Enriched) -- Video + Detections (~5s delay)

Each frame carries optional analytics when available.

| Transport | Endpoint | Query Params |
|-----------|----------|--------------|
| WebSocket MSE | `/api/cameras/ws/stream` | `cameraId=<id>`, `analytics=true` |

**Flow:**

1. Client connects with `cameraId=<id>&analytics=true`.
2. Query param parsed: `pkg/edgeview/ws_stream.go:90`.
3. `streamSession.start()` -> `startLive()` sets `analyticsMode = true`.
4. Session attaches to **Analytics Hub** via `hub.AnalyticsRelayHub().Consume()`.
5. Server sends `{"type":"mode_change","mode":"live"}`.
6. Packets delayed by ~5s (due to BlockingMerger waiting for analytics).
7. `pkt.Analytics` contains detections when available.

**Key differences from raw live:**

- Uses analytics relay hub instead of main hub (line 338-341 in ws_stream.go).
- `attachLiveConsumer()` checks `analyticsMode` and routes to analytics hub.
- BlockingMerger waits up to 7s for analytics per video packet.
- Falls back to no-analytics delivery if timeout expires.

**File locations:**

- Mode selection: `pkg/edgeview/ws_stream.go:attachLiveConsumer()` (line 331)
- Analytics hub creation: `internal/edge/app.go` (lines 187-199)
- BlockingMerger: `pkg/pva/blocking_merger.go`

### 3. Recorded Playback -- Historical fMP4 Segments

Plays from disk with optional analytics injection.

| Transport | Endpoint | Query Params |
|-----------|----------|--------------|
| WebSocket MSE | `/api/cameras/ws/stream` | `cameraId=<id>`, `start=<RFC3339>` |
| HTTP chunked fMP4 | `/api/cameras/{cameraId}/stream` | `start=<RFC3339>` |

**Flow:**

1. Client provides `start=2025-04-01T15:00:00Z` (RFC3339 timestamp).
2. `ResolvePlaybackStart()` determines if data exists:
   - **Recorded mode:** Segments exist in requested range.
   - **First-available mode:** No segments in range, fallback to earliest.
   - **Live mode:** Requested time is beyond latest recording.
3. Creates per-session `RelayHub` with `WithMaxConsumers(1)` (single consumer, blocking delivery).
4. `RecordedDemuxerFactory()` chains disk fMP4 segments with ChainingDemuxer.
5. In follow mode (no `to` param), transitions to live buffer when segments exhausted.

**Analytics enrichment for recorded playback:**

- Uses `AnalyticsRecordedDemuxerFactory()` when `analytics=true`.
- Wraps segments with `WallClockStampingDemuxer` for wall-clock sync.
- Uses `MetadataMerger` with `CompositeSource`:
  - **Primary:** `PersistenceSource` reads from SQLite analytics DB.
  - **Fallback:** `LiveAnalyticsStore` for near-live tail transition.
- Immediate (non-blocking) analytics injection (unlike analytics hub blocking).

**File locations:**

- Mode resolution: `pkg/edgeview/stream.go:ResolvePlaybackStart()` (line 257)
- Recorded factory: `pkg/edgeview/stream.go:RecordedDemuxerFactory()` (line 350)
- Analytics factory: `pkg/edgeview/stream.go:AnalyticsRecordedDemuxerFactory()` (line 402)

### 4. Analytics-Only (JSON Stream, No Video)

Pure analytics without video frames.

| Transport | Endpoint | Query Params |
|-----------|----------|--------------|
| WebSocket text | `/api/cameras/ws/analytics` | `cameraId=<id>` |

**Flow:**

1. Client connects to `/api/cameras/ws/analytics?cameraId=<id>`.
2. Subscribes to `AnalyticsHub` pub/sub.
3. Receives full `FrameAnalytics` JSON as text frames when analytics arrive.
4. Non-blocking: slow consumers drop frames.

**File locations:**

- Handler: `pkg/edgeview/ws_analytics.go` (line 21)
- Hub: `pkg/pva/store.go:AnalyticsHub` (line 146)

---

## Analytics Ingestion Pipeline

```
gRPC IngestAnalytics(sourceID, wallClock, FrameAnalytics)
  -> AnalyticsPipeline.Handle()              [store.go:244]
      -> AnalyticsStore.Put()                [time-indexed, 30s TTL]
      -> AnalyticsHub.Broadcast()            [wakes BlockingMerger subscribers]
      -> PersistenceWriter.Enqueue()         [optional, SQLite for recorded playback]
```

---

## Seek and Mode Switching

WebSocket clients can seek between live and recorded transparently via JSON commands:

| Command | Behavior |
|---------|----------|
| `{"type":"mse","value":"seek","time":"<RFC3339>"}` | Seek to absolute time |
| `{"type":"mse","value":"seek","time":"now"}` | Switch to live |
| `{"type":"mse","value":"skip","offset":"-30s"}` | Relative seek (Go duration) |
| `{"type":"mse","value":"pause"}` | Pause (recorded mode only) |
| `{"type":"mse","value":"resume"}` | Resume (recorded mode only) |

**Seek flow:**

1. Client sends seek command with `seq` (debounce counter).
2. `handleSeek()` or `handleSkip()` computes target time.
3. `executeSeek()`:
   - Stops current session (closes old consumer).
   - Calls `resolveAndStartSeek()` to determine new mode.
   - Creates new session (live or recorded).
   - Attaches new consumer.
   - Sends `{"type":"seeked","mode":"...","wallClock":"...","codecChanged":...}`.

**File locations:**

- Seek handler: `pkg/edgeview/ws_stream.go:handleSeek()` (line 500)
- Skip handler: `pkg/edgeview/ws_stream.go:handleSkip()` (line 554)

---

## Concurrent Viewing: User A (Video-Only) + User B (Analytics-Enriched)

### Scenario Setup

Camera `cam-1` is streaming live. Two users connect simultaneously:

- **User A** -- `GET /api/cameras/ws/stream?cameraId=cam-1` (no `analytics` param)
- **User B** -- `GET /api/cameras/ws/stream?cameraId=cam-1&analytics=true`

### Connection and Hub Routing

Both connections enter `wsStream()` at `ws_stream.go:61`. The divergence happens at **line 90**:

```go
analyticsMode := r.URL.Query().Get("analytics") == "true"
```

- User A: `analyticsMode = false`
- User B: `analyticsMode = true`

Both create a `streamSession` (line 92-97) and call `session.start(ctx, zero)` -> `startLive()` (line 162). Both receive:

```json
{"type":"mode_change","mode":"live","wallClock":"2026-04-05T..."}
```

When the client sends `{"type":"mse"}`, both reach `attachConsumer()` (line 236) -> `attachLiveConsumer()` (line 331). **This is where the paths diverge**:

```go
// ws_stream.go:337-342
hub := s.handler.svc.Hub()          // <- User A stays here (main RelayHub)
if s.analyticsMode {
    if aHub := s.handler.svc.AnalyticsRelayHub(); aHub != nil {
        hub = aHub                  // <- User B switches to analytics RelayHub
    }
}
```

Both then call `hub.Consume(ctx, "cam-1", opts)` on their respective hubs (line 344), and both get tracked via `TrackConsumer()` (line 351) -- so `ViewerCount()` returns **2**.

### Packet Flow: User A (Main Hub)

```
Camera -> RTSP -> avgrabber.Demuxer -> Main RelayHub relay
                                            |
                                       Fan-out (relay.go:588)
                                       /              \
                                    User A          Recorder
                                 (MSE consumer)   (fMP4 consumer)
```

- **Latency:** ~0ms from capture.
- **`pkt.Analytics`:** always `nil` -- no merger in this path.
- **Delivery:** With 2+ consumers on the relay (User A + recorder), the relay uses **leaky writes** (`relay.go:591-616`). If User A's WebSocket stalls beyond `wsWriteTimeout` (10s), frames are dropped and User A resyncs on the next keyframe.

### Packet Flow: User B (Analytics Hub)

```
Camera -> RTSP -> avgrabber.Demuxer -> Main RelayHub
                                            |
                                       PacketBuffer (30s GOP ring)
                                            |
                      ProxyMuxDemuxer reads from buffer at T-5s
                                            |
                                      BlockingMerger
                            (waits up to 7s per video packet
                             for analytics in AnalyticsStore)
                                            |
                                 Analytics RelayHub relay
                                            |
                                         User B
                                      (MSE consumer)
```

Per video packet, `BlockingMerger.ReadPacket()` (`blocking_merger.go:50-99`):

1. **Fast path** (line 62): `source.Fetch(pkt.FrameID, pkt.WallClockTime)` -> binary search in `AnalyticsStore` (+/-200ms tolerance, `store.go:78,82`). If found, inject `pkt.Analytics` immediately.
2. **Slow path** (line 69): subscribe to `AnalyticsHub`, wait up to `maxWait` (7s) for a `Broadcast()` notification, then re-check the store.
3. **Timeout** (line 86-87): return packet with `pkt.Analytics = nil` -- graceful degradation, no stall.

- **Latency:** ~5s behind live (configurable via `analytics_delay` in `config.go:41`).
- **`pkt.Analytics`:** populated with `*av.FrameAnalytics` when available, `nil` on timeout.
- **Delivery:** If User B is the only consumer on the analytics relay, delivery is **blocking** (back-pressure). If a second analytics viewer joins, it switches to **leaky writes**.

### Isolation Properties

| Property | Main Hub (User A) | Analytics Hub (User B) |
|---|---|---|
| **RelayHub instance** | `sm` -- created at app startup | `analyticsRelayHub` -- `app.go:196` |
| **Packet source** | Raw `avgrabber.Demuxer` | `ProxyMuxDemuxer` -> `BlockingMerger` |
| **Independent relay loop** | Yes -- `relay.go:588` fan-out loop | Yes -- separate relay instance, own fan-out loop |
| **Consumer count** | Shared with recorder + other non-analytics viewers | Only analytics viewers |
| **Leaky threshold** | 2+ consumers on this hub's relay | 2+ consumers on this hub's relay |

User A does not affect User B and vice versa. They consume from entirely separate `RelayHub` instances with independent relay goroutines. The only shared resource is the `PacketBuffer` (read-only ring buffer on the main hub) that the analytics hub's `ProxyMuxDemuxer` reads from -- but `PacketBuffer.Demuxer()` returns a snapshot reader, so it does not interfere with main hub consumers.

### Example: Frame at T=12:00:00.000

| Event | Wall Clock | User A sees | User B sees |
|---|---|---|---|
| Camera captures frame | `12:00:00.000` | -- | -- |
| Frame enters main hub | `12:00:00.001` | Receives frame (`Analytics: nil`) | -- |
| Analytics tool produces detection | `12:00:00.300` | -- | -- |
| gRPC `IngestAnalytics` -> `AnalyticsPipeline.Handle()` | `12:00:00.350` | -- | -- |
| `AnalyticsStore.Put("cam-1", 12:00:00.000, fa)` | `12:00:00.350` | -- | -- |
| `AnalyticsHub.Broadcast("cam-1", fa)` | `12:00:00.350` | -- | -- |
| Analytics hub reads frame from buffer (T-5s) | `12:00:05.001` | -- | -- |
| `BlockingMerger` fast-path: `store.lookup()` finds match | `12:00:05.001` | -- | Receives frame (`Analytics: {detections: [...]}`) |

User A saw the frame at T+1ms. User B saw the same frame at T+5001ms, enriched with detections.

### Example: Analytics Arrive Late (>5s after frame)

| Event | Wall Clock | User B sees |
|---|---|---|
| Camera captures frame | `12:00:00.000` | -- |
| Analytics hub reads frame from buffer | `12:00:05.001` | -- |
| `BlockingMerger` fast-path: `store.lookup()` -> `nil` | `12:00:05.001` | -- |
| `BlockingMerger` slow-path: subscribes to `AnalyticsHub` | `12:00:05.001` | -- |
| Analytics tool finally produces result | `12:00:08.000` | -- |
| `Broadcast` wakes merger -> re-checks store -> found | `12:00:08.000` | Receives frame (`Analytics: {detections: [...]}`) |

User B's frame was delayed by 8s total (5s hub delay + 3s blocking wait). User A was unaffected.

### Example: Analytics Never Arrive (timeout)

| Event | Wall Clock | User B sees |
|---|---|---|
| Camera captures frame | `12:00:00.000` | -- |
| Analytics hub reads frame from buffer | `12:00:05.001` | -- |
| `BlockingMerger` fast-path: miss | `12:00:05.001` | -- |
| `BlockingMerger` slow-path: subscribes, waits... | `12:00:05.001` | -- |
| `deadline.C` fires (`maxWait=7s`) | `12:00:12.001` | Receives frame (`Analytics: nil`) |

User B gets the frame after 12s with no analytics. The `BlockingMerger` returns the packet as-is on timeout (`blocking_merger.go:86-87`). No error, no stall -- next packet proceeds normally. User A was unaffected the entire time.

### Pause Isolation

If User A sends `{"type":"mse","value":"pause"}` (`ws_stream.go:362-378`):

```go
// ws_stream.go:370-372
if live {
    return  // <- NO-OP for live mode
}
```

The comment at line 369 explains: "Live mode: do NOT pause the shared relay -- it would stop packets for all consumers (including the recorder)." So User A's pause request is ignored at the server -- the MSE player handles it client-side. The main hub relay continues feeding the recorder and any other live viewers. User B is on an entirely separate hub, so User A's actions have zero effect.

---

## HTTP vs WebSocket: Feature Parity

| Feature | HTTP Live | HTTP Recorded | WebSocket Live | WebSocket Recorded |
|---------|-----------|---------------|-----------------|-------------------|
| Stream video | Yes | Yes | Yes | Yes |
| Analytics enrichment | No | No | Yes (`?analytics=true`) | Yes (`?analytics=true`) |
| Seek/skip | No | No | Yes | Yes |
| Pause/resume | No | No | No (live) | Yes (recorded) |

HTTP is single request-response; WebSocket allows bidirectional commands.

---

## Configuration

```json
{
  "analytics_delay": "5s",
  "analytics_max_wait": "7s"
}
```

| Parameter | Type | Default | Meaning |
|-----------|------|---------|---------|
| `analytics_delay` | Duration | `5s` | How far behind live the analytics hub reads |
| `analytics_max_wait` | Duration | `7s` | Max time per video packet waiting for analytics |

Set via JSON config file or environment variables loaded via `godotenv`.

---

## Key Files

### Edge View Package (`pkg/edgeview/`)

| File | Purpose |
|------|---------|
| `stream.go` | Service struct, ResolvePlaybackStart, RecordedDemuxerFactory, AnalyticsRecordedDemuxerFactory |
| `ws_stream.go` | Unified WebSocket streaming handler, session lifecycle, seek/skip logic |
| `http_handler.go` | HTTP routes, OpenAPI definitions, live/recorded/analytics endpoints |
| `ws_analytics.go` | Pure JSON analytics WebSocket stream |

### Analytics Pipeline (`pkg/pva/`)

| File | Purpose |
|------|---------|
| `store.go` | AnalyticsStore (time-indexed, 200ms match tolerance), AnalyticsHub (pub/sub) |
| `blocking_merger.go` | Blocks per video packet waiting for analytics (7s timeout) |
| `proxy.go` | ProxyMuxDemuxer, NewAnalyticsDemuxerFactory |
| `persistence_source.go` | PersistenceSource for SQLite analytics lookup |

### Edge App Initialization (`internal/edge/`)

| File | Purpose |
|------|---------|
| `app.go` (lines 187-199) | Creates and starts analytics relay hub |
| `config.go` (lines 41-45) | Configuration fields for analytics timing |

---

## Viewer Tracking

Both video-only and analytics-enriched viewers call `TrackConsumer()` (`stream.go:334`). The count is **not** split by hub -- `ViewerCount()` returns the total across both modes. Exposed on the `/health` endpoint as `activeViewers`.

---

## Observations

1. **HTTP has no analytics support** -- only WebSocket can request `analytics=true`. HTTP endpoints serve video-only for both live and recorded.
2. **Same `mode_change` message** -- both modes send `{"mode":"live"}`, so the client cannot distinguish analytics-enriched from raw based on the mode message alone.
3. **Graceful degradation** -- if analytics do not arrive within 7s, the packet passes through without them (no stall).
4. **Complete hub isolation** -- video-only and analytics-enriched viewers are on separate RelayHub instances with independent relay goroutines, fan-out loops, and delivery policies.
5. **Viewer count is aggregate** -- `ViewerCount()` does not distinguish between video-only and analytics viewers.
