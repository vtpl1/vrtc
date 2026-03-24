# MSE WebSocket API

This document describes the WebSocket-based MSE streaming API exposed at
`/v3/api/ws`.

The API is intended for browser clients that want to play a live stream using
the Media Source Extensions (MSE) API. The server delivers:

- a text codec string
- an fMP4 init segment
- a continuous stream of fMP4 media fragments
- optional JSON metadata messages

## Endpoint

```text
ws://<host>/v3/api/ws?producerID=<producer-id>&consumerID=<consumer-id>
```

Use `wss://` when the server is behind TLS.

### Query parameters

| Name | Required | Meaning |
|------|----------|---------|
| `producerID` | yes | Logical stream identifier. This is the source stream the server should attach to. |
| `consumerID` | yes | Client/session identifier. Must be unique per active consumer on the same producer. |

### Practical guidance

- Use a stable `producerID` for the same upstream source.
- Use a fresh `consumerID` per browser tab / player session, for example a UUID.
- Reusing an active `consumerID` for the same producer returns an error.

## Transport characteristics

- Protocol: WebSocket
- Path: `/v3/api/ws`
- Message directions:
  - client to server: JSON text commands
  - server to client: JSON text messages and binary fMP4 payloads
- WebSocket compression: disabled by the server
- Origin checks: currently permissive

## Client command format

Client commands are JSON text messages with this shape:

```json
{
  "type": "mse",
  "value": ""
}
```

### Supported commands

| `type` | `value` | Meaning |
|--------|---------|---------|
| `mse` | `""` | Start the MSE subscription. This is the required first command. |
| `mse` | `"pause"` | Pause upstream packet delivery for this producer if the source supports pausing. |
| `mse` | `"resume"` | Resume delivery after a previous pause. |

Notes:

- There is no explicit success acknowledgement for `pause` or `resume`.
- Unsupported or unknown commands are not part of the public contract.

## Server message types

The server sends two kinds of frames:

- text frames: JSON
- binary frames: fMP4 bytes

### 1. Codec/control text message

Shape:

```json
{
  "type": "mse",
  "value": "video/mp4; codecs=\"avc1.640028,mp4a.40.2\""
}
```

This message is sent:

- once at startup, before the first init segment
- again on mid-stream codec changes

The `value` string is intended to be passed to:

- `MediaSource.isTypeSupported(value)`
- `mediaSource.addSourceBuffer(value)`
- `sourceBuffer.changeType(value)` on codec changes

### 2. Error text message

Shape:

```json
{
  "type": "error",
  "error": "<human-readable error>"
}
```

Typical reasons:

- invalid or duplicate `consumerID`
- stream open failure
- muxer creation failure
- producer startup failure

In practice an error message is usually followed by connection shutdown.

### 3. Metadata text message

If the stream carries per-frame metadata, the server serializes that metadata
object directly as JSON and sends it as a text frame.

This is not wrapped in the `{ "type": "mse" }` envelope.

A deployment using PVA analytics typically sends a payload shaped like:

```json
{
  "siteId": 1,
  "channelId": 2,
  "timeStamp": 1710000000,
  "timeStampEnd": 1710000033,
  "timeStampEncoded": 1710000000,
  "frameId": 123456,
  "vehicleCount": 1,
  "peopleCount": 2,
  "refWidth": 1920,
  "refHeight": 1080,
  "objectList": []
}
```

For PVA-style metadata, the timestamp fields should be interpreted as:

- `timeStamp`: wall-clock timestamp
- `timeStampEncoded`: media timestamp associated with the encoded frame
- `timeStampEnd`: end timestamp for the same metadata interval when applicable

Client-side rule:

- if the text frame has `type: "mse"`, treat it as codec/control
- if it has `type: "error"`, treat it as a terminal server error
- otherwise treat it as metadata payload

### 4. Binary media messages

Binary frames contain fragmented MP4 data.

Expected sequence:

1. codec string text message
2. init segment binary message
3. media fragment binary messages

The init segment typically starts with `ftyp` or `moov`.
Subsequent media fragments typically start with `moof` or `mdat`.

### 5. Wall-clock time

The current binary fMP4 stream should be treated as a media timeline, not as a
wall-clock timeline.

For external clients, the public contract is:

- the fMP4 payload gives you media timing needed for playback
- the fMP4 payload does not currently expose a public wall-clock timestamp per
  fragment or sample

That means a client should not expect to recover absolute wall-clock capture
time from the binary MP4 bytes alone.

If your integration needs wall-clock time, use one of these approaches:

- consume the parallel JSON metadata channel and read the timestamp fields
  provided there
- maintain an application-level side channel that maps media events to absolute
  time

For PVA-style metadata, use:

- `timeStamp` as the wall-clock time
- `timeStampEncoded` as the media timestamp

If wall-clock time is required as part of the binary media contract in a future
version, it should be specified explicitly as a protocol addition rather than
inferred from the current fragments.

## Connection sequence

Normal startup flow:

1. Client opens the WebSocket with `producerID` and `consumerID`.
2. Client sends `{"type":"mse","value":""}`.
3. Server attaches the consumer to the producer.
4. Server sends a codec string text message.
5. Server sends an fMP4 init segment.
6. Server sends fMP4 media fragments as they are produced.
7. Optional metadata text frames may appear between binary frames.

Codec-change flow:

1. Server sends a new `{ "type": "mse", "value": "<new codec string>" }`.
2. Client should update the existing `SourceBuffer` with `changeType(...)` if supported.
3. Server sends codec-change binary payload / subsequent fragments using the new codec state.

Shutdown flow:

1. Client closes the WebSocket, or the server encounters an error.
2. The server closes the muxer and detaches the consumer automatically.

## Browser consumption guide

The minimal browser strategy is:

1. create a `MediaSource`
2. connect a WebSocket
3. send the MSE subscription command
4. wait for the codec string
5. create a `SourceBuffer`
6. queue incoming binary frames and append them serially
7. handle codec-change text messages with `changeType(...)`

### Minimal example

```html
<video id="player" controls autoplay muted playsinline></video>
<script>
const producerID = "camera-1";
const consumerID = crypto.randomUUID();
const wsURL = `ws://${location.host}/v3/api/ws?producerID=${encodeURIComponent(producerID)}&consumerID=${encodeURIComponent(consumerID)}`;

const video = document.getElementById("player");
const mediaSource = new MediaSource();
video.src = URL.createObjectURL(mediaSource);

const ws = new WebSocket(wsURL);
ws.binaryType = "arraybuffer";

let sourceBuffer = null;
let codecString = "";
const queue = [];

function appendNext() {
  if (!sourceBuffer || sourceBuffer.updating || queue.length === 0) {
    return;
  }
  sourceBuffer.appendBuffer(queue.shift());
}

mediaSource.addEventListener("sourceopen", () => {
  ws.send(JSON.stringify({ type: "mse", value: "" }));
});

ws.addEventListener("message", async (event) => {
  if (typeof event.data !== "string") {
    const bytes = new Uint8Array(event.data);
    queue.push(bytes);
    appendNext();
    return;
  }

  const msg = JSON.parse(event.data);

  if (msg.type === "error") {
    console.error("stream error:", msg.error);
    ws.close();
    return;
  }

  if (msg.type === "mse") {
    codecString = msg.value;

    if (!MediaSource.isTypeSupported(codecString)) {
      throw new Error(`Unsupported codec string: ${codecString}`);
    }

    if (!sourceBuffer) {
      sourceBuffer = mediaSource.addSourceBuffer(codecString);
      sourceBuffer.addEventListener("updateend", appendNext);
    } else if (typeof sourceBuffer.changeType === "function") {
      sourceBuffer.changeType(codecString);
    } else {
      console.warn("Codec changed but changeType() is unavailable");
    }

    return;
  }

  // Any other JSON text message is optional metadata.
  console.debug("metadata:", msg);
});

ws.addEventListener("close", () => {
  console.log("stream closed");
});
</script>
```

### Recommended client behavior

- Set `ws.binaryType = "arraybuffer"`.
- Serialize `appendBuffer` calls through a queue.
- Check `MediaSource.isTypeSupported(codecString)` before creating the source buffer.
- Keep the `consumerID` unique per session.
- Treat the first binary frame as the init segment.
- Expect metadata text frames at any time after startup.
- Do not assume the binary fMP4 payload carries wall-clock capture time.
- Handle codec-change notifications with `SourceBuffer.changeType()` when available.

## Pause and resume

To pause:

```json
{"type":"mse","value":"pause"}
```

To resume:

```json
{"type":"mse","value":"resume"}
```

Notes:

- These commands affect the producer, not just one browser tab.
- They only have an effect when the upstream source supports pause and resume.
- If pause/resume is unsupported, the server treats the request as a no-op.

## Error handling

Server-side consume/setup errors are returned as:

```json
{
  "type": "error",
  "error": "..."
}
```

Recommended client strategy:

- log the error string
- close the socket
- generate a new `consumerID`
- reconnect with backoff if appropriate

## Operational notes

- The WebSocket API is transport-oriented. It does not currently define
  authentication, authorization, or session resumption.
