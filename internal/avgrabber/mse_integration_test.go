//go:build cgo

// MSE-over-WebSocket integration test for the avgrabber RTSP demuxer pipeline.
//
// This test spins up an in-process HTTP test server backed by a real StreamManager
// and avgrabber RTSP demuxer, connects a WebSocket client, negotiates the MSE
// protocol, and verifies that:
//   - The server sends a text codec string (video/mp4; codecs=…).
//   - The server sends a binary fMP4 init segment (ftyp or moov box).
//   - The server sends at least minMSEFragments binary fMP4 media fragments (moof box).
//
// Run with:
//
//	go test -v -count=1 -run MSE ./internal/avgrabber/
//
// Skip in CI by passing -short.
package avgrabber_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/vtpl1/vrtc/internal/avgrabber"
	"github.com/vtpl1/vrtc/internal/httprouter"
	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/streammanager3"
)

const (
	mseConsumerID   = "test-mse-consumer"
	minMSEFragments = 5
	mseTimeout      = 30 * time.Second
)

// mseMessage mirrors the JSON envelope sent over the WebSocket text channel.
type mseMessage struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// TestMSE_RTSP_Pipeline verifies the full RTSP → StreamManager → MSEWriter →
// WebSocket client pipeline.
func TestMSE_RTSP_Pipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping MSE RTSP integration test in short mode")
	}

	avgrabber.Init()
	t.Cleanup(avgrabber.Deinit)

	ctx, cancel := context.WithTimeout(t.Context(), mseTimeout)
	defer cancel()

	// ── build stream manager ──────────────────────────────────────────────────

	cfg := rtspConfig()

	demuxerFactory := av.DemuxerFactory(
		func(_ context.Context, _ string) (av.DemuxCloser, error) {
			return avgrabber.NewDemuxer(cfg)
		},
	)

	demuxerRemover := av.DemuxerRemover(func(_ context.Context, _ string) error { return nil })

	sm := streammanager3.New(demuxerFactory, demuxerRemover)

	if err := sm.Start(ctx); err != nil {
		t.Fatalf("StreamManager.Start: %v", err)
	}

	t.Cleanup(func() { _ = sm.Stop() })

	// ── HTTP test server with WebSocket endpoint ───────────────────────────────

	srv := httptest.NewServer(httprouter.NewRouter(ctx, sm))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v3/api/ws" +
		"?producerID=" + testRTSPURL +
		"&consumerID=" + mseConsumerID

	// ── dial WebSocket ────────────────────────────────────────────────────────

	wsConn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}

	defer wsConn.CloseNow() //nolint:errcheck

	// Raise the read limit — fMP4 fragments can easily exceed the 32 KB default.
	wsConn.SetReadLimit(4 * 1024 * 1024)

	// ── send MSE subscription command ────────────────────────────────────────

	sub, err := json.Marshal(mseMessage{Type: "mse", Value: ""})
	if err != nil {
		t.Fatalf("marshal subscription: %v", err)
	}

	if err := wsConn.Write(ctx, websocket.MessageText, sub); err != nil {
		t.Fatalf("write subscription: %v", err)
	}

	// ── receive and validate messages ────────────────────────────────────────

	var (
		gotCodecStr bool
		gotInitSeg  bool
		fragments   int
	)

	for !gotCodecStr || !gotInitSeg || fragments < minMSEFragments {
		if ctx.Err() != nil {
			break
		}

		kind, data, err := wsConn.Read(ctx)
		if err != nil {
			t.Fatalf("Read after codecStr=%v initSeg=%v fragments=%d: %v",
				gotCodecStr, gotInitSeg, fragments, err)
		}

		switch kind {
		case websocket.MessageText:
			var msg mseMessage
			if jerr := json.Unmarshal(data, &msg); jerr != nil {
				t.Errorf("unmarshal text message: %v", jerr)

				continue
			}

			if msg.Type == "mse" && !gotCodecStr {
				gotCodecStr = true

				if !strings.HasPrefix(msg.Value, "video/mp4; codecs=") {
					t.Errorf("codec string %q does not start with 'video/mp4; codecs='", msg.Value)
				}

				t.Logf("codec string: %s", msg.Value)
			}

		case websocket.MessageBinary:
			if len(data) < 8 {
				t.Errorf("binary message too short (%d bytes)", len(data))

				continue
			}

			boxType := string(data[4:8])
			boxSize := binary.BigEndian.Uint32(data[0:4])

			if !gotInitSeg {
				// First binary message must be an fMP4 init segment.
				// Init segments start with an ftyp or moov box.
				switch boxType {
				case "ftyp", "moov":
					gotInitSeg = true
					t.Logf("init segment: box=%s size=%d total_bytes=%d", boxType, boxSize, len(data))
				default:
					t.Errorf("expected init segment (ftyp/moov), got box type %q", boxType)
				}
			} else {
				// Subsequent binary messages must be fMP4 media fragments.
				if boxType == "moof" || boxType == "mdat" {
					fragments++
					t.Logf("fragment %d: box=%s size=%d", fragments, boxType, boxSize)
				}
				// Ignore any other box types silently.
			}
		}
	}

	// ── assertions ───────────────────────────────────────────────────────────

	if !gotCodecStr {
		t.Error("no MSE codec string received within timeout")
	}

	if !gotInitSeg {
		t.Error("no fMP4 init segment received within timeout")
	}

	if fragments < minMSEFragments {
		t.Errorf("only %d fMP4 fragments received, want >=%d", fragments, minMSEFragments)
	}

	t.Logf("MSE pipeline OK: codec=%v init=%v fragments=%d", gotCodecStr, gotInitSeg, fragments)
}
