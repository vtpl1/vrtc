# Recorded View Flow Audit: Analytics-Enriched vs Video/Audio Only

## Architecture: Per-Session Isolated RelayHub

Unlike the live view flow (which uses two shared hubs), recorded playback creates a **dedicated per-session `RelayHub`** with `WithMaxConsumers(1)` for every viewer. This enforces:

- **Single-consumer isolation** -- delivery is always blocking (back-pressure), never leaky.
- **Independent read position** -- each viewer has their own ChainingDemuxer cursor through the fMP4 segments.
- **Independent lifecycle** -- pause/resume/seek affect only that session.

| | Video/Audio Only (default) | Analytics-Enriched (`?analytics=true`) |
|---|---|---|
| **RelayHub** | Per-session, `WithMaxConsumers(1)` | Per-session, `WithMaxConsumers(1)` |
| **DemuxerFactory** | `RecordedDemuxerFactory` | `AnalyticsRecordedDemuxerFactory` |
| **Segment wrapping** | None | `WallClockStampingDemuxer` per segment |
| **Chain wrapping** | None | `MetadataMerger` with `CompositeSource` |
| **Analytics lookup** | N/A | Non-blocking: `PersistenceSource` (SQLite) + `LiveAnalyticsStore` fallback |
| **Latency** | Disk I/O only | Disk I/O + SQLite batch lookup (~1 query per 10s of video) |
| **HTTP support** | Yes | No (WebSocket only) |

---

## Recorded View Modes

### 1. Recorded (Video Only) -- Plain Segment Playback

| Transport | Endpoint | Query Params |
|-----------|----------|--------------|
| WebSocket MSE | `/api/cameras/ws/stream` | `cameraId=<id>`, `start=<RFC3339>` |
| HTTP chunked fMP4 | `/api/cameras/{cameraId}/stream` | `start=<RFC3339>` |

**WebSocket Flow:**

1. Client connects with `cameraId=cam-1&start=2026-04-01T10:00:00Z`.
2. `wsStream()` parses `start` at `ws_stream.go:69` -- non-zero, so recorded mode.
3. `session.start(ctx, start)` -> `startResolved()` at `ws_stream.go:183`.
4. `ResolvePlaybackStart()` (`stream.go:257`) determines mode:
   - Queries `recIndex.LastAvailable()` to check if `start` is beyond recordings.
   - Queries `recIndex.QueryByChannel()` for segments covering the range.
   - Returns one of: `recorded`, `first_available`, or `live`.
5. If `recorded` or `first_available`, sends playback_info to client:
   ```json
   {"type":"playback_info","actualStartWallClock":"...","wallClock":"...","mode":"recorded"}
   ```
6. `recordedFactory(from)` at `ws_stream.go:743` -- `analyticsMode` is false, so selects `RecordedDemuxerFactory` (`stream.go:350`).
7. Creates per-session `RelayHub` with `WithMaxConsumers(1)` at `ws_stream.go:216`.
8. Client sends `{"type":"mse"}` -> `attachConsumer()` -> `playSM.Consume()` at `ws_stream.go:267`.
9. `ChainingDemuxer` reads fMP4 segments sequentially via `indexSource.Next()`.
10. In follow mode (`to` is zero), transitions to live `PacketBuffer` when segments are exhausted.

**HTTP Flow:**

1. `httpStream()` at `http_handler.go:868` parses `start` param.
2. Non-zero start -> `httpStreamRecorded()` at `http_handler.go:933`.
3. Creates `RecordedDemuxerFactory` directly (line 942) -- no analytics support on HTTP.
4. Creates per-session `RelayHub` with `WithMaxConsumers(1)` (line 941-945).
5. Creates `fmp4.NewMuxer` writing to the HTTP response body (line 960-965).
6. `playSM.Consume()` at line 970 -- single consumer, blocking delivery.
7. Streams until client disconnects (`ctx.Done()`) or muxer error.

**File locations:**

- WebSocket: `pkg/edgeview/ws_stream.go:startResolved()` (line 183), `recordedFactory()` (line 743)
- HTTP: `pkg/edgeview/http_handler.go:httpStreamRecorded()` (line 933)
- Factory: `pkg/edgeview/stream.go:RecordedDemuxerFactory()` (line 350)
- Chaining: `indexSource.Next()` (`stream.go:620`)

### 2. Recorded (Analytics-Enriched) -- Segment Playback with Detection Overlay

| Transport | Endpoint | Query Params |
|-----------|----------|--------------|
| WebSocket MSE | `/api/cameras/ws/stream` | `cameraId=<id>`, `start=<RFC3339>`, `analytics=true` |

**Flow:**

1. Client connects with `cameraId=cam-1&start=2026-04-01T10:00:00Z&analytics=true`.
2. `ws_stream.go:90` parses `analyticsMode = true`.
3. `startResolved()` -> `ResolvePlaybackStart()` same as video-only.
4. `recordedFactory(from)` at `ws_stream.go:743` checks analytics:
   ```go
   if s.analyticsMode && svc.PersistReader() != nil {
       return svc.AnalyticsRecordedDemuxerFactory(
           s.cameraID, from, time.Time{},
           svc.PersistReader(),
           svc.LiveAnalyticsSource(s.cameraID),
       )
   }
   ```
5. `AnalyticsRecordedDemuxerFactory` (`stream.go:402`) builds the enriched pipeline:
   - `openFirstSegment(entries, true)` wraps the first segment with `WallClockStampingDemuxer` (`stream.go:506-507`).
   - `indexSource` is created with `wallClockStamp: true` (line 447) so all subsequent segments are also wrapped.
   - The entire chain is wrapped with `MetadataMerger` using a `CompositeSource` (line 452).
6. Per-session `RelayHub` with `WithMaxConsumers(1)` -- same isolation as video-only.
7. Per packet, `MetadataMerger.ReadPacket()` (`merger.go:36-47`):
   - Calls `source.Fetch(pkt.FrameID, pkt.WallClockTime)`.
   - **Non-blocking** -- returns immediately with analytics or `nil`.
   - Unlike live analytics-enriched (which uses `BlockingMerger` with up to 7s wait), recorded playback uses `MetadataMerger` with **immediate** lookup.

**File locations:**

- Factory selection: `pkg/edgeview/ws_stream.go:recordedFactory()` (line 743)
- Factory: `pkg/edgeview/stream.go:AnalyticsRecordedDemuxerFactory()` (line 402)
- Wall-clock stamping: `pkg/pva/wallclock_demuxer.go` (line 32)
- Merger: `pkg/pva/merger.go:ReadPacket()` (line 36)
- Composite source: `pkg/pva/persistence_source.go:CompositeSource` (line 197)

---

## Segment Chaining Pipeline

Both modes use `ChainingDemuxer` backed by `indexSource` (`stream.go:519`). The difference is whether segments are wall-clock-stamped and whether the chain is wrapped.

### Video-Only Pipeline

```
SQLite RecordingIndex
    |
    | QueryByChannel(channelID, from, to)
    v
[segment_001.fmp4] -> [segment_002.fmp4] -> ... -> [segment_N.fmp4]
    |                     |                              |
    | fmp4.Demuxer        | fmp4.Demuxer                 | fmp4.Demuxer
    v                     v                              v
              ChainingDemuxer (DTS adjustment at boundaries)
                              |
                              v
                     Per-session RelayHub
                     (MaxConsumers=1, blocking)
                              |
                              v
                     fMP4 Muxer -> WebSocket/HTTP
```

### Analytics-Enriched Pipeline

```
SQLite RecordingIndex
    |
    | QueryByChannel(channelID, from, to)
    v
[segment_001.fmp4] -> [segment_002.fmp4] -> ... -> [segment_N.fmp4]
    |                     |                              |
    | fmp4.Demuxer        | fmp4.Demuxer                 | fmp4.Demuxer
    |                     |                              |
    | WallClockStamping   | WallClockStamping            | WallClockStamping
    | (segStart + DTS)    | (segStart + DTS)             | (segStart + DTS)
    v                     v                              v
              ChainingDemuxer (DTS adjustment at boundaries)
                              |
                              v
                      MetadataMerger
                              |
                    +---------+---------+
                    |                   |
             PersistenceSource    LiveAnalyticsStore
             (SQLite, primary)    (in-memory, fallback)
                              |
                              v
                     Per-session RelayHub
                     (MaxConsumers=1, blocking)
                              |
                              v
                     MSE Muxer -> WebSocket
```

---

## Analytics Lookup: PersistenceSource

`PersistenceSource` (`persistence_source.go:31`) is the main analytics source for recorded playback. Unlike the live path's `BlockingMerger` (which waits for analytics to arrive), this does an **immediate non-blocking** lookup against SQLite:

```go
// merger.go:42-44 — called per packet, non-blocking
if fa := m.source.Fetch(pkt.FrameID, pkt.WallClockTime); fa != nil {
    pkt.Analytics = fa
}
```

`PersistenceSource.Fetch()` (`persistence_source.go:52-79`):

1. **Fast path** (line 60-67): check in-memory cache under read lock. If `targetMS` falls within `[cacheFrom, cacheTo)`, binary search the cache (line 111-147, +/-200ms tolerance).
2. **Slow path** (line 72): cache miss -> `reload(wallClock)` queries SQLite for a 10-second window (`cacheWindowHalf = 5s`, so `[wallClock-5s, wallClock+5s)`). Up to 1000 frames per query.
3. Return matched `*av.FrameAnalytics` or `nil`.

**Batch caching** amortizes SQLite cost: sequential playback at 25 FPS incurs ~1 SQLite round-trip per 250 frames (10 seconds of video).

### CompositeSource for Follow-Mode Transition

When recorded playback exhausts disk segments and transitions to the live `PacketBuffer` (follow mode), the `CompositeSource` (`persistence_source.go:197-213`) handles the analytics source switch:

```go
// persistence_source.go:203-210
func (c *CompositeSource) Fetch(frameID int64, wallClock time.Time) *FrameAnalytics {
    if fa := c.Primary.Fetch(frameID, wallClock); fa != nil {
        return fa  // SQLite has it (recent enough to be persisted)
    }
    if c.Fallback != nil {
        return c.Fallback.Fetch(frameID, wallClock)  // Try live AnalyticsStore
    }
    return nil
}
```

- **Primary:** `PersistenceSource` -- SQLite, covers all persisted history.
- **Fallback:** `LiveAnalyticsStore` (in-memory, 30s TTL) -- covers the near-live tail where analytics may not yet be flushed to SQLite.

---

## WallClockStampingDemuxer

The key enabler for analytics matching on recorded playback. Disk fMP4 segments contain only codec-relative DTS/PTS, not wall-clock time. `WallClockStampingDemuxer` (`wallclock_demuxer.go:23`) stamps each packet:

```go
// wallclock_demuxer.go:54-60
if pkt.WallClockTime.IsZero() && !d.segmentStart.IsZero() {
    if !d.baseDTSSet {
        d.baseDTS = pkt.DTS
        d.baseDTSSet = true
    }
    pkt.WallClockTime = d.segmentStart.Add(pkt.DTS - d.baseDTS)
}
```

- `segmentStart` comes from `RecordingEntry.StartTime` (set when the segment was recorded).
- `baseDTS` is captured from the first packet to handle non-zero starting DTS.
- Formula: `wallClock = segmentStart + (pkt.DTS - baseDTS)`.
- Packets that already have a `WallClockTime` (e.g. from the live buffer during follow-mode transition) pass through unchanged.

This wrapping happens per segment in `openFirstSegment()` (`stream.go:506`) and `openEntry()` (`stream.go:717`), **before** `ChainingDemuxer` adjusts DTS offsets.

---

## Follow Mode and Live Transition

Both factories pass `to = time.Time{}` (zero), enabling follow mode (`stream.go:351,408`).

When `indexSource.Next()` (`stream.go:620`) exhausts all known disk segments:

1. **Quick poll** (`waitForNextWithTimeout`, line 727): one immediate query for new segments from the recording index. If the recorder has written new segments since the factory was created, they are picked up.
2. **Live transition** (`tryLiveTransition`, line 660): if no new segments are found, transitions to the `PacketBuffer`:
   ```go
   // stream.go:674-683
   since := time.Now().Add(-30 * time.Second)  // overlap generously
   if len(s.entries) > 0 {
       since = s.entries[len(s.entries)-1].EndTime
   }
   return s.liveBuf.Demuxer(since)
   ```
   The 30-second overlap (or last segment end time) ensures no frames are missed during the transition.
3. **Continued disk polling** (`waitForNext`, line 655): if no buffer is available, continues polling the recording index at 1-second intervals (`pollInterval`, line 544).

For analytics-enriched follow mode, the `MetadataMerger` wrapping the chain continues to call `CompositeSource.Fetch()`. During the live-buffer tail, `PersistenceSource` (SQLite) may not yet have the analytics, so `CompositeSource` falls back to `LiveAnalyticsStore` (in-memory).

---

## Gap Detection

`indexSource` implements `chain.GapDetector` (`stream.go:552`). In `openEntry()` (line 688):

```go
// stream.go:702-711
if !s.lastSegEnd.IsZero() && !entry.StartTime.IsZero() {
    gap := entry.StartTime.Sub(s.lastSegEnd)
    if gap >= gapThreshold {  // gapThreshold = 5s (line 547)
        s.lastGap = gap
    }
}
```

When a gap >= 5 seconds is detected between consecutive segments, `ChainingDemuxer` sets `IsDiscontinuity` on the first packet of the new segment. The client uses this to handle visual discontinuities (e.g. reset the MSE buffer).

During seek, `resolveAndStartSeek()` (`ws_stream.go:649`) also detects gaps:

```go
// ws_stream.go:676-678
if resolvedFrom.Sub(seekTime) >= gapThreshold {
    gap = true
}
```

This is communicated to the client in the `seeked` response (`ws_stream.go:702-715`).

---

## Seek and Mode Switching

Recorded sessions support seek, skip, pause, and resume. All commands are processed in `readLoop()` (`ws_stream.go:431`).

### Seek Flow (`handleSeek`, line 500)

1. Seq-based debounce: discard stale seeks (line 503-504).
2. Special value `"now"` -> stop session, `startLive()`, attach consumer.
3. Parse RFC3339 time -> `executeSeek()` (line 591).
4. `executeSeek()`:
   - `stop(ctx)` -- closes current consumer, stops per-session hub (line 600).
   - `resolveAndStartSeek()` -- resolves mode, creates new session (line 602).
   - Check for stale seq again before expensive attach (line 608-614).
   - `attachConsumer()` -- attaches to new session (line 617).
   - Detect codec change by comparing MIME strings (line 626-627).
   - Send `seeked` response with mode, wallClock, codecChanged, gap (line 629).

### Skip Flow (`handleSkip`, line 554)

1. Parse Go duration offset (e.g. `"-30s"`, `"60s"`).
2. Compute target: `base + offset` where base is `lastSeekTime` (or `now` if unset).
3. Delegate to `executeSeek()`.

### Pause/Resume (`ws_stream.go:362-393`)

```go
// ws_stream.go:362-378
func (s *streamSession) pause(ctx context.Context) {
    if live { return }  // NO-OP for live -- client handles pause locally
    if sm != nil { sm.PauseRelay(ctx, s.cameraID) }
}
```

- **Recorded mode:** `PauseRelay` pauses the per-session relay's read loop -- the `ChainingDemuxer` stops reading packets. Resume restarts it.
- **Live mode:** Pause is a no-op at the server. The shared relay cannot be paused without affecting all consumers (including the recorder). The MSE player handles pause locally.

---

## Concurrent Viewing: User A (Video-Only) + User B (Analytics-Enriched)

### Scenario Setup

Camera `cam-1` has recordings from `10:00:00` to `10:30:00`. Two users connect simultaneously:

- **User A** -- `GET /api/cameras/ws/stream?cameraId=cam-1&start=2026-04-01T10:00:00Z`
- **User B** -- `GET /api/cameras/ws/stream?cameraId=cam-1&start=2026-04-01T10:00:00Z&analytics=true`

### Hub Creation

Both connections create **separate** per-session `RelayHub` instances at `ws_stream.go:216` (via `startResolved`) or `ws_stream.go:722` (via `startRecordedAt`):

```go
sm := relayhub.New(factory, nil, relayhub.WithMaxConsumers(1))
```

- **User A's hub** wraps `RecordedDemuxerFactory` -- plain segment chain.
- **User B's hub** wraps `AnalyticsRecordedDemuxerFactory` -- stamped + merged chain.

Each hub has its own relay goroutine, consumer, and `ChainingDemuxer` instance.

### Packet Flow: User A (Video Only)

```
RecordingIndex.QueryByChannel("cam-1", 10:00:00, zero)
    |
    | returns [seg_001, seg_002, ..., seg_N]
    v
seg_001.fmp4 -> fmp4.Demuxer
    |
    v
ChainingDemuxer (indexSource, follow=true)
    |
    v
Per-session RelayHub A (MaxConsumers=1, blocking)
    |
    v
MSE Writer -> WebSocket -> User A
    |
    | pkt.Analytics = nil (always)
    | pkt.WallClockTime = zero (no stamping)
```

- Segments are read as raw fMP4 -- no `WallClockStampingDemuxer`, no `MetadataMerger`.
- `pkt.Analytics` is always `nil`.
- `pkt.WallClockTime` is zero (wall-clock is not needed without analytics matching).
- Delivery is blocking (single consumer) -- no frame drops.

### Packet Flow: User B (Analytics-Enriched)

```
RecordingIndex.QueryByChannel("cam-1", 10:00:00, zero)
    |
    | returns [seg_001, seg_002, ..., seg_N]
    v
seg_001.fmp4 -> fmp4.Demuxer -> WallClockStampingDemuxer(10:00:00)
    |
    v
ChainingDemuxer (indexSource, follow=true, wallClockStamp=true)
    |
    v
MetadataMerger
    |
    +-- CompositeSource.Fetch(frameID, wallClock)
    |       |
    |       +-- PersistenceSource.Fetch()     [primary: SQLite cache]
    |       |       |
    |       |       +-- cache hit? binary search, +/-200ms -> return
    |       |       +-- cache miss? reload(wallClock) -> SQLite query
    |       |           [10s window, up to 1000 frames]
    |       |
    |       +-- LiveAnalyticsStore.Fetch()    [fallback: in-memory, 30s TTL]
    |               (only used during follow-mode live-buffer tail)
    |
    v
Per-session RelayHub B (MaxConsumers=1, blocking)
    |
    v
MSE Writer -> WebSocket -> User B
    |
    | pkt.Analytics = *av.FrameAnalytics (when available)
    | pkt.WallClockTime = segStart + (DTS - baseDTS)
```

- Each segment is wrapped with `WallClockStampingDemuxer` so packets have `WallClockTime`.
- `MetadataMerger` does an immediate (non-blocking) lookup per packet.
- `PersistenceSource` batch-caches 10s of analytics from SQLite -- ~1 query per 250 frames at 25 FPS.
- Delivery is blocking (single consumer) -- no frame drops.

### Isolation Properties

| Property | User A | User B |
|---|---|---|
| **RelayHub** | Own instance (A) | Own instance (B) |
| **ChainingDemuxer** | Own instance, own cursor | Own instance, own cursor |
| **Segment file handles** | Own `fmp4.Demuxer` per segment | Own `fmp4.Demuxer` per segment |
| **Read position** | Independent | Independent |
| **Pause/resume** | Pauses relay A only | Pauses relay B only |
| **Seek** | Stops A, creates new hub A' | Stops B, creates new hub B' |
| **Delivery** | Blocking (MaxConsumers=1) | Blocking (MaxConsumers=1) |
| **pkt.Analytics** | Always `nil` | Populated or `nil` |
| **pkt.WallClockTime** | Zero | Stamped |
| **SQLite RecordingIndex** | Shared (read-only queries) | Shared (read-only queries) |
| **SQLite Analytics DB** | Not accessed | Accessed via PersistenceSource |

User A and User B are **completely independent**. They share only read-only access to the SQLite recording index and segment files on disk. Their hubs, demuxers, and read cursors are separate.

### Concrete Example: Frames at T=10:05:00

| Event | User A sees | User B sees |
|---|---|---|
| Both sessions reach segment covering 10:05:00 | -- | -- |
| User A: `fmp4.Demuxer.ReadPacket()` returns frame | Frame (`Analytics: nil`, `WallClock: zero`) | -- |
| User B: `WallClockStampingDemuxer.ReadPacket()` stamps packet | -- | `pkt.WallClockTime = 10:05:00.000` |
| User B: `MetadataMerger.ReadPacket()` calls `source.Fetch()` | -- | -- |
| User B: `PersistenceSource` cache hit (within 10s window) | -- | Frame (`Analytics: {detections: [...]}`) |

Both see the frame at approximately the same time (limited only by disk I/O). User B has a slight overhead from the SQLite cache lookup (~microseconds on cache hit).

### Example: User A Seeks, User B Unaffected

| Event | User A | User B |
|---|---|---|
| User A sends `{"type":"mse","value":"seek","time":"2026-04-01T10:20:00Z"}` | -- | -- |
| `executeSeek()`: stops relay A, closes consumer A | Stops streaming | Still streaming 10:05:xx |
| `resolveAndStartSeek()`: creates new relay A' at 10:20:00 | -- | Still streaming 10:05:xx |
| `attachConsumer()`: attaches to relay A' | Receives frame at 10:20:00 | Still streaming 10:05:xx |
| Sends seeked response | `{"type":"seeked","mode":"recorded","wallClock":"..."}` | -- |

User B continues playing from their own position, completely unaffected by User A's seek.

### Example: User B Pauses, User A Unaffected

| Event | User A | User B |
|---|---|---|
| User B sends `{"type":"mse","value":"pause"}` | -- | -- |
| `pause()`: `sm.PauseRelay(ctx, "cam-1")` on relay B | Still streaming | ChainingDemuxer B stops reading |
| 30 seconds pass | Streaming at 10:05:30 | Paused at 10:05:00 |
| User B sends `{"type":"mse","value":"resume"}` | -- | -- |
| `resume()`: `sm.ResumeRelay(ctx, "cam-1")` on relay B | Still streaming | Resumes from 10:05:00 |

User A is entirely unaffected because it has its own relay, own demuxer chain, and own consumer.

### Example: Follow-Mode Live Transition (Both Users)

Assume both users started playback at 10:25:00 and recording ends at 10:30:00:

| Event | User A | User B |
|---|---|---|
| Both exhaust disk segments at 10:30:00 | -- | -- |
| `indexSource.Next()` -> `waitForNextWithTimeout()` -> no new segments | -- | -- |
| `tryLiveTransition()`: returns `PacketBuffer.Demuxer(10:30:00)` | Transitions to live buffer | Transitions to live buffer |
| User A: packets from buffer have `WallClockTime` set (buffer stamps) | Receives live frames (`Analytics: nil`) | -- |
| User B: `MetadataMerger` calls `CompositeSource.Fetch()` | -- | -- |
| User B: `PersistenceSource` -> nil (too recent for SQLite flush) | -- | -- |
| User B: `LiveAnalyticsStore.Fetch()` -> found in memory | -- | Receives live frames (`Analytics: {detections: [...]}`) |

The `CompositeSource` fallback ensures analytics continuity during the recorded-to-live transition. If the live `AnalyticsStore` also has no match (e.g., analytics tool is down), the frame passes through with `pkt.Analytics = nil`.

---

## HTTP vs WebSocket: Recorded Playback

| Feature | HTTP Recorded | WebSocket Recorded |
|---------|---------------|-------------------|
| Stream video | Yes | Yes |
| Analytics enrichment | No | Yes (`?analytics=true`) |
| Seek/skip | No | Yes |
| Pause/resume | No | Yes |
| Mode switching (seek to live) | No | Yes (seek `"now"`) |
| Codec change detection | No | Yes (seekedResponse) |
| Gap notification | No | Yes (seekedResponse.gap) |

The HTTP recorded handler (`http_handler.go:933-992`) always uses `RecordedDemuxerFactory` (no analytics), creates a per-session hub, and streams until the client disconnects. It does not accept any commands.

---

## Viewer Tracking

**Recorded playback sessions are NOT tracked by `TrackConsumer()`.** The `TrackConsumer()` call happens only in `attachLiveConsumer()` (`ws_stream.go:351`), which is the live-mode path. Recorded sessions go through `playSM.Consume()` at `ws_stream.go:267` without calling `TrackConsumer()`.

This means `ViewerCount()` only counts live viewers. Recorded playback viewers are invisible to the viewer count. The HTTP live handler does call `TrackConsumer()` (`http_handler.go:904`), but the HTTP recorded handler (`http_handler.go:933`) does not.

---

## Configuration

Recorded playback uses the same configuration as live:

| Parameter | Default | Effect on Recorded |
|-----------|---------|-------------------|
| `recording_index_path` | (required) | Path to per-channel SQLite recording indexes |
| `analytics_delay` | `5s` | No direct effect -- recorded uses non-blocking `MetadataMerger` |
| `analytics_max_wait` | `7s` | No direct effect -- only used by live `BlockingMerger` |

The `PersistenceSource` has its own constants:
- `cacheWindowHalf = 5s` (`persistence_source.go:14`) -- half the SQLite batch query window.
- `persistMatchTolerance = 200ms` (`persistence_source.go:19`) -- wall-clock match tolerance for analytics lookup.

---

## Key Files

### Recorded Playback

| File | Purpose |
|------|---------|
| `pkg/edgeview/ws_stream.go` | `startResolved()` (line 183), `startRecordedAt()` (line 720), `recordedFactory()` (line 743), seek/skip/pause/resume |
| `pkg/edgeview/stream.go` | `ResolvePlaybackStart()` (line 257), `RecordedDemuxerFactory()` (line 350), `AnalyticsRecordedDemuxerFactory()` (line 402), `indexSource` (line 519) |
| `pkg/edgeview/http_handler.go` | `httpStreamRecorded()` (line 933) |

### Analytics Enrichment (Recorded)

| File | Purpose |
|------|---------|
| `pkg/pva/merger.go` | `MetadataMerger` -- non-blocking per-packet analytics injection |
| `pkg/pva/wallclock_demuxer.go` | `WallClockStampingDemuxer` -- stamps packets with `segmentStart + DTS` |
| `pkg/pva/persistence_source.go` | `PersistenceSource` (SQLite batch cache), `CompositeSource` (primary + fallback) |

### Shared Infrastructure

| File | Purpose |
|------|---------|
| `pkg/recorder/index_sqlite.go` | `RecordingIndex` -- per-channel SQLite, `QueryByChannel`, `FirstAvailable`, `LastAvailable` |
| `vrtc-sdk/av/chain/` | `ChainingDemuxer` -- chains fMP4 segments, DTS adjustment, gap detection |
| `vrtc-sdk/av/relayhub/relay.go` | Relay fan-out loop, `WithMaxConsumers(1)` enforcement |

---

## Summary: Recorded vs Live Analytics Enrichment

| Aspect | Live Analytics-Enriched | Recorded Analytics-Enriched |
|--------|------------------------|----------------------------|
| **Hub** | Shared `analyticsRelayHub` | Per-session, `WithMaxConsumers(1)` |
| **Merger** | `BlockingMerger` (blocks up to 7s) | `MetadataMerger` (immediate, non-blocking) |
| **Analytics source** | `AnalyticsStore` (in-memory, 30s TTL) | `CompositeSource`: `PersistenceSource` (SQLite) + `LiveAnalyticsStore` fallback |
| **WallClock stamping** | Done by `ProxyMuxDemuxer`'s inner chain | `WallClockStampingDemuxer` per segment |
| **Latency overhead** | ~5s delay + up to 7s blocking | ~microseconds (SQLite cache hit) to ~milliseconds (cache miss reload) |
| **Delivery** | Leaky (if 2+ analytics viewers) | Always blocking (single consumer) |
| **Pause support** | No (live can't pause) | Yes (`PauseRelay` on per-session hub) |
| **Seek support** | Seek to `"now"` only | Full seek/skip with mode resolution |
