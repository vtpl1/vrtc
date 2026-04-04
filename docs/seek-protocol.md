# Unified Streaming & Seek Protocol

This document describes the WebSocket-based unified streaming API exposed at
`/api/cameras/ws/stream`. It supports live streaming, recorded playback, and
seek operations with codec-change detection.

**Audience:** Frontend developers building a camera playback UI with MSE.

---

## Endpoint

```text
ws://<host>/api/cameras/ws/stream?camera_id=<id>&start=<RFC3339>
```

### Query Parameters

| Name | Required | Meaning |
|------|----------|---------|
| `camera_id` | yes | Camera/channel identifier |
| `start` | no | RFC3339 timestamp. Omit for live mode; provide for recorded playback. |

### Examples

```text
# Live mode
ws://localhost:8080/api/cameras/ws/stream?camera_id=cam-01

# Recorded playback from a specific time
ws://localhost:8080/api/cameras/ws/stream?camera_id=cam-01&start=2026-04-04T14:00:00Z
```

---

## Connection Lifecycle

```
Client                                 Server
  │                                       │
  │── WS connect ────────────────────────>│
  │                                       │ resolve playback mode
  │<── mode_change / playback_info ──────│
  │                                       │
  │── {"type":"mse"} ───────────────────>│ attach consumer
  │                                       │
  │<── {"type":"mse","value":"codecs"} ──│ codec string (text)
  │<── [binary] init segment ────────────│ ftyp + moov
  │<── [binary] moof+mdat ──────────────│ media fragments
  │<── [binary] moof+mdat ──────────────│
  │    ...                                │
  │                                       │
  │── seek / skip / pause / resume ─────>│
  │                                       │
```

---

## Client Commands (Client → Server)

All commands are JSON text frames with `"type": "mse"`.

### Start Streaming

```json
{"type": "mse"}
```

Must be the first command after connection. Triggers consumer attachment and
starts media delivery.

### Pause / Resume

```json
{"type": "mse", "value": "pause"}
{"type": "mse", "value": "resume"}
```

Pause only affects recorded playback. In live mode, pause is handled
client-side (the server does not stop sending).

### Absolute Seek

```json
{
  "type": "mse",
  "value": "seek",
  "time": "2026-04-04T14:32:00Z",
  "seq": 42
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `time` | string | yes | RFC3339 timestamp, or `"now"` to switch to live |
| `seq` | int64 | no | Monotonic counter for debouncing. Server discards seeks with `seq` <= the last seen `seq`. |

**Behavior:**
- Stops the current playback session
- Resolves the target time against the recording index
- Starts a new session at the resolved position
- Sends a `seeked` response before media data resumes

**Special value `"now"`:** Switches to live mode immediately.

### Relative Seek (Skip)

```json
{
  "type": "mse",
  "value": "skip",
  "offset": "-30s",
  "seq": 43
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `offset` | string | yes | Go duration string (e.g. `"-30s"`, `"60s"`, `"-2m"`, `"1h"`) |
| `seq` | int64 | no | Monotonic counter for debouncing |

Computes `target = lastSeekTime + offset`, then performs an absolute seek.

---

## Server Messages (Server → Client)

### 1. `mode_change` — Initial Mode

Sent immediately after connection when starting in live mode.

```json
{"type": "mode_change", "mode": "live"}
```

### 2. `playback_info` — Resolved Start Position

Sent when starting in recorded mode.

```json
{
  "type": "playback_info",
  "actualStart": "2026-04-04T14:00:02Z",
  "mode": "recorded"
}
```

| Field | Values |
|-------|--------|
| `mode` | `"recorded"` — recordings found at requested time |
| | `"first_available"` — no recordings at requested time, fell back to earliest |

### 3. `mse` — Codec String

```json
{"type": "mse", "value": "video/mp4; codecs=\"avc1.64001E,flac\""}
```

Sent before the init segment. Use this value with:
- `MediaSource.isTypeSupported(value)`
- `mediaSource.addSourceBuffer(value)`
- `sourceBuffer.changeType(value)` on codec changes

### 4. `seeked` — Seek Completed

```json
{
  "type": "seeked",
  "time": "2026-04-04T14:31:58Z",
  "mode": "recorded",
  "codecChanged": false,
  "codecs": "",
  "gap": false,
  "seq": 42
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"seeked"` |
| `time` | string | Actual wall-clock time landed on (RFC3339). May differ from requested time due to keyframe alignment or gaps. |
| `mode` | string | `"recorded"`, `"live"`, or `"first_available"` |
| `codecChanged` | bool | `true` if the new position has different codecs than the previous position |
| `codecs` | string | New MIME codec string. Present only when `codecChanged` is `true`. |
| `gap` | bool | `true` if the seek target fell in a recording gap and was snapped to the next available segment |
| `seq` | int64 | Echoed from the request. Use to correlate responses with requests. |

**After receiving `seeked`:**
- A new `mse` codec string text message follows
- A new init segment (binary) follows
- Media fragments (binary) follow

### 5. `error` — Error

```json
{"type": "error", "error": "human-readable message"}
```

### 6. Binary Frames — fMP4 Media

Binary WebSocket frames contain fMP4 data:
1. Init segment (`ftyp` + `moov`) — after codec string
2. Media fragments (`moof` + `mdat`) — continuous stream

---

## Seek Scenarios

### Seek Into Recorded Footage

```
Client: {"type":"mse","value":"seek","time":"2026-04-04T14:32:00Z","seq":1}
Server: {"type":"seeked","time":"2026-04-04T14:31:58Z","mode":"recorded","codecChanged":false,"seq":1}
Server: {"type":"mse","value":"video/mp4; codecs=\"avc1.64001E\""}
Server: [binary] init segment
Server: [binary] moof+mdat (from keyframe at 14:31:58)
```

### Seek Into a Gap

```
Client: {"type":"mse","value":"seek","time":"2026-04-04T14:33:00Z","seq":2}
Server: {"type":"seeked","time":"2026-04-04T14:35:00Z","mode":"recorded","codecChanged":false,"gap":true,"seq":2}
Server: {"type":"mse","value":"video/mp4; codecs=\"avc1.64001E\""}
Server: [binary] init segment
Server: [binary] moof+mdat (from next available segment)
```

### Seek Beyond Recordings (Switch to Live)

```
Client: {"type":"mse","value":"seek","time":"2026-04-04T15:00:00Z","seq":3}
Server: {"type":"seeked","time":"2026-04-04T14:59:55Z","mode":"live","codecChanged":false,"seq":3}
Server: {"type":"mse","value":"video/mp4; codecs=\"avc1.64001E\""}
Server: [binary] init segment
Server: [binary] live moof+mdat fragments
```

### Seek With Codec Change

```
Client: {"type":"mse","value":"seek","time":"2026-03-28T10:00:00Z","seq":4}
Server: {"type":"seeked","time":"2026-03-28T10:00:02Z","mode":"recorded","codecChanged":true,"codecs":"video/mp4; codecs=\"hev1.1.6.L153.B0\"","seq":4}
Server: {"type":"mse","value":"video/mp4; codecs=\"hev1.1.6.L153.B0\""}
Server: [binary] init segment (H.265)
Server: [binary] moof+mdat
```

### Switch to Live

```
Client: {"type":"mse","value":"seek","time":"now","seq":5}
Server: {"type":"seeked","time":"2026-04-04T14:59:55Z","mode":"live","codecChanged":false,"seq":5}
Server: {"type":"mse","value":"video/mp4; codecs=\"avc1.64001E\""}
Server: [binary] init segment
Server: [binary] live fragments
```

### Skip (Relative Seek)

```
Client: {"type":"mse","value":"skip","offset":"-30s","seq":6}
Server: {"type":"seeked","time":"2026-04-04T14:31:30Z","mode":"recorded","codecChanged":false,"seq":6}
Server: {"type":"mse","value":"video/mp4; codecs=\"avc1.64001E\""}
Server: [binary] init segment
Server: [binary] moof+mdat
```

---

## Codec Change During Continuous Playback

When playback crosses a segment boundary with different codecs (e.g., the camera
was reconfigured), the server sends a new `mse` codec string followed by a new
init segment — the same flow as a normal codec change in the MSE protocol.

```
Server: [binary] moof+mdat (last fragment of old codec)
Server: {"type":"mse","value":"video/mp4; codecs=\"hev1.1.6.L153.B0\""}
Server: [binary] new init segment + codec-change fragment
Server: [binary] moof+mdat (new codec)
```

The client should call `sourceBuffer.changeType(newCodecString)` when it
receives a new `mse` codec string that differs from the current one.

**Optimization:** The server suppresses codec-change messages when the upstream
refreshes SPS/PPS in-band without actually changing codec parameters (same
profile, level, and resolution). This prevents unnecessary SourceBuffer resets.

---

## Client-Side Implementation Guide

### MSE Setup

```javascript
const video = document.getElementById("player");
const mediaSource = new MediaSource();
video.src = URL.createObjectURL(mediaSource);

let sourceBuffer = null;
let currentCodecStr = "";
const queue = [];
let seekSeq = 0; // monotonic counter
```

### WebSocket Message Handler

```javascript
ws.binaryType = "arraybuffer";

ws.onmessage = (event) => {
  if (typeof event.data !== "string") {
    // Binary: init segment or media fragment
    queue.push(new Uint8Array(event.data));
    appendNext();
    return;
  }

  const msg = JSON.parse(event.data);

  switch (msg.type) {
    case "mse":
      handleCodecString(msg.value);
      break;
    case "seeked":
      handleSeeked(msg);
      break;
    case "mode_change":
      handleModeChange(msg.mode);
      break;
    case "playback_info":
      handlePlaybackInfo(msg);
      break;
    case "error":
      console.error("Server error:", msg.error);
      break;
    default:
      // Metadata (analytics, etc.)
      handleMetadata(msg);
  }
};
```

### Handling Codec Strings

```javascript
function handleCodecString(codecStr) {
  if (!MediaSource.isTypeSupported(codecStr)) {
    console.error("Unsupported codec:", codecStr);
    return;
  }

  if (!sourceBuffer) {
    // First time: create SourceBuffer
    sourceBuffer = mediaSource.addSourceBuffer(codecStr);
    sourceBuffer.addEventListener("updateend", appendNext);
    currentCodecStr = codecStr;
    return;
  }

  if (codecStr !== currentCodecStr) {
    // Codec changed: update SourceBuffer type
    if (typeof sourceBuffer.changeType === "function") {
      sourceBuffer.changeType(codecStr);
    } else {
      // Fallback: recreate SourceBuffer
      mediaSource.removeSourceBuffer(sourceBuffer);
      sourceBuffer = mediaSource.addSourceBuffer(codecStr);
      sourceBuffer.addEventListener("updateend", appendNext);
    }
    currentCodecStr = codecStr;
  }
}
```

### Handling Seek Response

```javascript
function handleSeeked(msg) {
  // Ignore responses from stale seeks
  if (msg.seq < seekSeq) return;

  console.log(`Seeked to ${msg.time}, mode=${msg.mode}, gap=${msg.gap}`);

  if (msg.codecChanged && msg.codecs) {
    // Codec will change — the next mse message + init segment will handle it
    console.log("Codec changed to:", msg.codecs);
  }

  // Update UI: playhead position, mode indicator, etc.
  updatePlayhead(msg.time, msg.mode);
}
```

### Performing a Seek

```javascript
async function seekTo(timeISO) {
  seekSeq++;

  // 1. Abort any in-flight append
  if (sourceBuffer && sourceBuffer.updating) {
    sourceBuffer.abort();
  }

  // 2. Clear old buffered data
  if (sourceBuffer && sourceBuffer.buffered.length > 0) {
    await waitForUpdateEnd(sourceBuffer);
    sourceBuffer.remove(0, Infinity);
    await waitForUpdateEnd(sourceBuffer);
  }

  // 3. Clear the append queue
  queue.length = 0;

  // 4. Send seek command
  ws.send(JSON.stringify({
    type: "mse",
    value: "seek",
    time: timeISO,
    seq: seekSeq
  }));

  // After this, the server will send:
  //   1. seeked response (text)
  //   2. mse codec string (text)
  //   3. init segment (binary)
  //   4. media fragments (binary)
  //
  // The existing onmessage handler processes all of these.
}

function skipBy(durationStr) {
  seekSeq++;

  if (sourceBuffer && sourceBuffer.updating) {
    sourceBuffer.abort();
  }

  queue.length = 0;

  ws.send(JSON.stringify({
    type: "mse",
    value: "skip",
    offset: durationStr,
    seq: seekSeq
  }));
}
```

### Buffer Management

During long playback sessions, periodically evict old data behind the playhead
to stay within browser memory limits:

```javascript
// Run every 10 seconds
function evictOldData() {
  if (!sourceBuffer || sourceBuffer.updating) return;
  if (sourceBuffer.buffered.length === 0) return;

  const behind = video.currentTime - 30; // keep 30s behind playhead
  if (behind > 0 && sourceBuffer.buffered.start(0) < behind) {
    sourceBuffer.remove(sourceBuffer.buffered.start(0), behind);
  }
}

setInterval(evictOldData, 10000);
```

### Helper: Wait for SourceBuffer Update

```javascript
function waitForUpdateEnd(sb) {
  return new Promise((resolve) => {
    if (!sb.updating) { resolve(); return; }
    sb.addEventListener("updateend", resolve, { once: true });
  });
}
```

### Append Queue

```javascript
function appendNext() {
  if (!sourceBuffer || sourceBuffer.updating || queue.length === 0) return;

  try {
    sourceBuffer.appendBuffer(queue.shift());
  } catch (e) {
    if (e.name === "QuotaExceededError") {
      // Buffer full — evict old data and retry
      evictOldData();
    } else {
      console.error("appendBuffer error:", e);
    }
  }
}
```

---

## Timeline API

To render a recording availability bar, use one of the current camera REST endpoints:

```
GET /api/cameras/{camera_id}/timeline?start=<RFC3339>&end=<RFC3339>
GET /api/cameras/{camera_id}/recordings?start=<RFC3339>&end=<RFC3339>
```

Returns an array of recording entries:

```json
[
  {
    "id": "rec-001",
    "channel_id": "cam-01",
    "start_time": "2026-04-04T09:00:00Z",
    "end_time": "2026-04-04T14:30:00Z",
    "status": "complete",
    "has_motion": true,
    "has_objects": false
  },
  {
    "id": "rec-002",
    "channel_id": "cam-01",
    "start_time": "2026-04-04T14:35:00Z",
    "end_time": "2026-04-04T16:00:00Z",
    "status": "complete",
    "has_motion": false,
    "has_objects": true
  }
]
```

Gaps between entries represent periods with no recording. The client should
render these as empty/grey regions on the timeline bar.

---

## Seq-Based Debouncing

When the user is dragging the timeline scrubber, the client fires many seek
commands in rapid succession. The `seq` field prevents seek pile-up:

1. Client increments `seekSeq` for each seek request
2. Server tracks the highest `seq` seen; discards seeks with `seq <= lastSeen`
3. Only the response for the latest `seq` reaches the client
4. Client ignores `seeked` responses where `msg.seq < seekSeq`

**Example: rapid scrubbing**

```javascript
// User drags scrubber — fires on mouseup, not on every pixel
scrubber.addEventListener("change", (e) => {
  const time = scrubber.valueToTime(e.target.value);
  seekTo(time.toISOString());
});
```

---

## Edge Cases

| Scenario | Server Behavior | Client Action |
|----------|----------------|---------------|
| Seek into a gap | Snaps to next available segment; `gap: true` | Show "no recording" indicator, resume playback |
| Seek before all recordings | Falls back to earliest; `mode: "first_available"` | Update playhead to actual start |
| Seek past latest recording | Switches to live; `mode: "live"` | Switch UI to live mode |
| Segment deleted by retention mid-seek | Server returns error | Retry seek or show error |
| Rapid successive seeks | Server discards stale `seq` values | Only process latest `seeked` response |
| Codec change on seek | `codecChanged: true` with `codecs` field | Call `changeType()` then append new init segment |
| Codec change mid-playback | New `mse` codec string text message | Call `changeType()` — no seek needed |
| Audio track added/removed | New `mse` codec string reflects track change | Call `changeType()` with updated codec string |

---

## Browser Compatibility Notes

| Feature | Chrome | Firefox | Safari | Edge |
|---------|--------|---------|--------|------|
| MSE fMP4 | Yes | Yes | Yes | Yes |
| `changeType()` | 70+ | 63+ | 15.4+ | 79+ |
| Auto gap-jumping | <3s gaps | Less aggressive | Least tolerant | Same as Chrome |
| Buffer quota | ~150 MB | ~100 MB | ~290 MB | ~150 MB |

- Always implement your own gap detection; don't rely on browser auto-jumping.
- Handle `QuotaExceededError` by evicting old data behind the playhead.
- Test `remove()` behavior: it respects GOP boundaries and may remove more or
  less than requested.

---

## Complete Example: Playback with Seek

```html
<!DOCTYPE html>
<html>
<head><title>Camera Playback</title></head>
<body>
  <video id="player" controls autoplay muted playsinline></video>
  <div>
    <button onclick="skipBy('-30s')">-30s</button>
    <button onclick="skipBy('30s')">+30s</button>
    <button onclick="seekTo('now')">Live</button>
    <input type="range" id="scrubber" min="0" max="86400" value="0"
           onchange="seekToScrubber(this.value)">
  </div>

  <script>
  const cameraId = "cam-01";
  const startTime = "2026-04-04T09:00:00Z";
  const wsURL = `ws://${location.host}/api/cameras/ws/stream`
    + `?camera_id=${encodeURIComponent(cameraId)}`
    + `&start=${encodeURIComponent(startTime)}`;

  const video = document.getElementById("player");
  const mediaSource = new MediaSource();
  video.src = URL.createObjectURL(mediaSource);

  const ws = new WebSocket(wsURL);
  ws.binaryType = "arraybuffer";

  let sourceBuffer = null;
  let currentCodecStr = "";
  const queue = [];
  let seekSeq = 0;

  function appendNext() {
    if (!sourceBuffer || sourceBuffer.updating || queue.length === 0) return;
    try {
      sourceBuffer.appendBuffer(queue.shift());
    } catch (e) {
      if (e.name === "QuotaExceededError") evictOldData();
      else console.error("append error:", e);
    }
  }

  function waitForUpdateEnd(sb) {
    return new Promise(r => {
      if (!sb.updating) { r(); return; }
      sb.addEventListener("updateend", r, { once: true });
    });
  }

  function evictOldData() {
    if (!sourceBuffer || sourceBuffer.updating) return;
    if (sourceBuffer.buffered.length === 0) return;
    const behind = video.currentTime - 30;
    if (behind > 0) sourceBuffer.remove(sourceBuffer.buffered.start(0), behind);
  }
  setInterval(evictOldData, 10000);

  mediaSource.addEventListener("sourceopen", () => {
    ws.send(JSON.stringify({ type: "mse", value: "" }));
  });

  ws.onmessage = (event) => {
    if (typeof event.data !== "string") {
      queue.push(new Uint8Array(event.data));
      appendNext();
      return;
    }

    const msg = JSON.parse(event.data);

    switch (msg.type) {
      case "mse":
        if (!MediaSource.isTypeSupported(msg.value)) {
          console.error("Unsupported:", msg.value);
          return;
        }
        if (!sourceBuffer) {
          sourceBuffer = mediaSource.addSourceBuffer(msg.value);
          sourceBuffer.addEventListener("updateend", appendNext);
        } else if (msg.value !== currentCodecStr) {
          if (typeof sourceBuffer.changeType === "function") {
            sourceBuffer.changeType(msg.value);
          }
        }
        currentCodecStr = msg.value;
        break;

      case "seeked":
        if (msg.seq >= seekSeq) {
          console.log(`Seeked: time=${msg.time} mode=${msg.mode} gap=${msg.gap} codecChanged=${msg.codecChanged}`);
        }
        break;

      case "error":
        console.error("Server:", msg.error);
        break;

      default:
        // Analytics metadata
        break;
    }
  };

  async function seekTo(timeOrNow) {
    seekSeq++;
    if (sourceBuffer?.updating) sourceBuffer.abort();
    if (sourceBuffer?.buffered.length > 0) {
      await waitForUpdateEnd(sourceBuffer);
      sourceBuffer.remove(0, Infinity);
      await waitForUpdateEnd(sourceBuffer);
    }
    queue.length = 0;

    ws.send(JSON.stringify({
      type: "mse", value: "seek",
      time: timeOrNow, seq: seekSeq
    }));
  }

  function skipBy(offset) {
    seekSeq++;
    if (sourceBuffer?.updating) sourceBuffer.abort();
    queue.length = 0;

    ws.send(JSON.stringify({
      type: "mse", value: "skip",
      offset: offset, seq: seekSeq
    }));
  }

  function seekToScrubber(val) {
    // Map scrubber value (0-86400 = seconds in a day) to absolute time
    const base = new Date(startTime);
    base.setSeconds(base.getSeconds() + parseInt(val));
    seekTo(base.toISOString());
  }
  </script>
</body>
</html>
```

---

## Message Reference (Summary)

### Client → Server

| Value | Fields | Description |
|-------|--------|-------------|
| `""` | | Start streaming (required first command) |
| `"pause"` | | Pause recorded playback |
| `"resume"` | | Resume recorded playback |
| `"seek"` | `time`, `seq` | Absolute seek to wall-clock time |
| `"skip"` | `offset`, `seq` | Relative seek by duration |

### Server → Client (Text)

| `type` | Description |
|--------|-------------|
| `mse` | Codec string — use with `addSourceBuffer` / `changeType` |
| `seeked` | Seek completed — contains time, mode, codecChanged, gap, seq |
| `mode_change` | Initial mode notification (live) |
| `playback_info` | Initial mode notification (recorded/first_available) with actual start |
| `error` | Error message |

### Server → Client (Binary)

| Order | Content |
|-------|---------|
| 1st after codec string | fMP4 init segment (`ftyp` + `moov`) |
| Subsequent | fMP4 media fragments (`moof` + `mdat`) |
