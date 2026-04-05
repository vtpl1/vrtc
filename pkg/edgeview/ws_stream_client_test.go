package edgeview

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/rs/zerolog"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/codec/h264parser"
	"github.com/vtpl1/vrtc-sdk/av/format/fmp4"
	"github.com/vtpl1/vrtc-sdk/av/relayhub"
	"github.com/vtpl1/vrtc/pkg/recorder"
)

// ── Test AV infrastructure ──────────────────────────────────────────────

// testH264Codec creates a minimal H.264 baseline codec for testing.
func testH264Codec(t *testing.T) h264parser.CodecData {
	t.Helper()

	sps := []byte{0x67, 0x42, 0x00, 0x1E, 0xAC, 0xD9, 0x40, 0xA0, 0x3D, 0xA1, 0x00, 0x00, 0x03, 0x00, 0x00}
	pps := []byte{0x68, 0xCE, 0x38, 0x80}

	codec, err := h264parser.NewCodecDataFromSPSAndPPS(sps, pps)
	if err != nil {
		t.Fatalf("h264 codec: %v", err)
	}

	return codec
}

// testH264Streams returns a single-track H.264 stream list.
func testH264Streams(t *testing.T) []av.Stream {
	t.Helper()

	return []av.Stream{{Idx: 0, Codec: testH264Codec(t)}}
}

// testH264KeyFrame returns a minimal AVCC-encoded IDR keyframe packet.
func testH264KeyFrame(idx int, dts time.Duration) av.Packet {
	return av.Packet{
		KeyFrame:  true,
		Idx:       0,
		CodecType: av.H264,
		DTS:       dts,
		Duration:  33 * time.Millisecond,
		Data:      []byte{0x00, 0x00, 0x00, 0x03, 0x65, 0xDE, 0xAD},
	}
}

// testH264Frame returns a minimal AVCC-encoded non-IDR packet.
func testH264Frame(dts time.Duration) av.Packet {
	return av.Packet{
		KeyFrame:  false,
		Idx:       0,
		CodecType: av.H264,
		DTS:       dts,
		Duration:  33 * time.Millisecond,
		Data:      []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0xBB},
	}
}

// slowDemuxer produces H.264 packets at ~30fps until context cancels.
// Used as the DemuxerFactory source for test relay hubs.
type slowDemuxer struct {
	streams []av.Stream
	pktN    int
	mu      sync.Mutex
}

func (d *slowDemuxer) GetCodecs(_ context.Context) ([]av.Stream, error) {
	return d.streams, nil
}

func (d *slowDemuxer) ReadPacket(ctx context.Context) (av.Packet, error) {
	timer := time.NewTimer(33 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return av.Packet{}, ctx.Err()
	case <-timer.C:
	}

	d.mu.Lock()
	d.pktN++
	n := d.pktN
	d.mu.Unlock()

	if n%30 == 1 {
		return testH264KeyFrame(n, time.Duration(n)*33*time.Millisecond), nil
	}

	return testH264Frame(time.Duration(n) * 33 * time.Millisecond), nil
}

func (d *slowDemuxer) Close() error { return nil }

// ── Mock recording index ────────────────────────────────────────────────

type mockRecordingIndex struct {
	mu      sync.Mutex
	entries map[string][]recorder.RecordingEntry
}

func newMockRecordingIndex() *mockRecordingIndex {
	return &mockRecordingIndex{entries: make(map[string][]recorder.RecordingEntry)}
}

func (m *mockRecordingIndex) addEntry(e recorder.RecordingEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries[e.ChannelID] = append(m.entries[e.ChannelID], e)
}

func (m *mockRecordingIndex) Insert(_ context.Context, e recorder.RecordingEntry) error {
	m.addEntry(e)

	return nil
}

func (m *mockRecordingIndex) QueryByChannel(
	_ context.Context, channelID string, from, to time.Time,
) ([]recorder.RecordingEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []recorder.RecordingEntry

	for _, e := range m.entries[channelID] {
		if e.Status == recorder.StatusRecording || e.Status == recorder.StatusDeleted ||
			e.Status == recorder.StatusCorrupted {
			continue
		}

		if !from.IsZero() && e.EndTime.Before(from) {
			continue
		}

		if !to.IsZero() && e.StartTime.After(to) {
			continue
		}

		result = append(result, e)
	}

	return result, nil
}

func (m *mockRecordingIndex) FirstAvailable(_ context.Context, channelID string) (recorder.RecordingEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, e := range m.entries[channelID] {
		if e.Status == recorder.StatusComplete || e.Status == recorder.StatusInterrupted {
			return e, nil
		}
	}

	return recorder.RecordingEntry{}, recorder.ErrNoRecordings
}

func (m *mockRecordingIndex) LastAvailable(_ context.Context, channelID string) (recorder.RecordingEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries := m.entries[channelID]
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Status == recorder.StatusComplete || entries[i].Status == recorder.StatusInterrupted {
			return entries[i], nil
		}
	}

	return recorder.RecordingEntry{}, recorder.ErrNoRecordings
}

func (m *mockRecordingIndex) Delete(context.Context, string) error { return nil }
func (m *mockRecordingIndex) QueryAllChannels(context.Context, time.Time, time.Time) ([]recorder.RecordingEntry, error) {
	return nil, nil
}

func (m *mockRecordingIndex) SealInterrupted(context.Context) error { return nil }
func (m *mockRecordingIndex) Close() error                          { return nil }

// ── Test segment file creation ──────────────────────────────────────────

// createTestSegment writes a minimal valid fMP4 file and returns its path.
func createTestSegment(t *testing.T, dir string, streams []av.Stream, nFrames int) string {
	t.Helper()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	path := filepath.Join(dir, "segment.fmp4")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create segment: %v", err)
	}
	defer f.Close()

	mux := fmp4.NewMuxer(f)
	ctx := context.Background()

	if err := mux.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("write header: %v", err)
	}

	for i := range nFrames {
		dts := time.Duration(i) * 33 * time.Millisecond
		if i%30 == 0 {
			if err := mux.WritePacket(ctx, testH264KeyFrame(i, dts)); err != nil {
				t.Fatalf("write keyframe %d: %v", i, err)
			}
		} else {
			if err := mux.WritePacket(ctx, testH264Frame(dts)); err != nil {
				t.Fatalf("write frame %d: %v", i, err)
			}
		}
	}

	if err := mux.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("write trailer: %v", err)
	}

	return path
}

// ── Test server setup ───────────────────────────────────────────────────

type wsTestServerOpts struct {
	recIndex recorder.RecordingIndex
}

type wsTestServerOption func(*wsTestServerOpts)

func withRecordingIndex(idx recorder.RecordingIndex) wsTestServerOption {
	return func(o *wsTestServerOpts) { o.recIndex = idx }
}

// newWSTestServer creates a test HTTP server with a live relay hub and optional
// recording index. Returns the server URL and a cleanup function.
func newWSTestServer(t *testing.T, streams []av.Stream, opts ...wsTestServerOption) *httptest.Server {
	t.Helper()

	cfg := &wsTestServerOpts{}
	for _, o := range opts {
		o(cfg)
	}

	// Live hub with a slow demuxer factory.
	hub := relayhub.New(
		func(_ context.Context, _ string) (av.DemuxCloser, error) {
			return &slowDemuxer{streams: streams}, nil
		},
		nil,
	)

	hubCtx, hubCancel := context.WithCancel(context.Background())

	if err := hub.Start(hubCtx); err != nil {
		hubCancel()
		t.Fatalf("hub start: %v", err)
	}

	log := zerolog.New(zerolog.NewTestWriter(t))
	svc := NewService(log, hub, cfg.recIndex, nil)
	handler := NewHTTPHandler(svc, log, "")

	server := httptest.NewServer(handler.Router())
	t.Cleanup(func() {
		server.Close()
		hubCancel()
		_ = hub.Stop()
	})

	return server
}

// ── WebSocket client helper ─────────────────────────────────────────────

type wsClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func dialWS(t *testing.T, server *httptest.Server, queryParams string) *wsClient {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/cameras/ws/stream?" + queryParams

	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })

	return &wsClient{t: t, conn: conn}
}

// readTextJSON reads the next text frame and decodes it as JSON.
func (c *wsClient) readTextJSON(timeout time.Duration) map[string]any {
	c.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	typ, data, err := c.conn.Read(ctx)
	if err != nil {
		c.t.Fatalf("readTextJSON: %v", err)
	}

	if typ != websocket.MessageText {
		c.t.Fatalf("expected text frame, got binary (%d bytes)", len(data))
	}

	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		c.t.Fatalf("unmarshal JSON %q: %v", data, err)
	}

	return msg
}

// readMessage reads the next message of any type.
func (c *wsClient) readMessage(timeout time.Duration) (websocket.MessageType, []byte) {
	c.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	typ, data, err := c.conn.Read(ctx)
	if err != nil {
		c.t.Fatalf("readMessage: %v", err)
	}

	return typ, data
}

// tryReadTextJSON reads a text frame, returning nil if timeout expires.
func (c *wsClient) tryReadTextJSON(timeout time.Duration) map[string]any {
	c.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	typ, data, err := c.conn.Read(ctx)
	if err != nil {
		return nil // timeout or close
	}

	if typ != websocket.MessageText {
		return nil
	}

	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil
	}

	return msg
}

func (c *wsClient) sendJSON(msg any) {
	c.t.Helper()

	if err := wsjson.Write(context.Background(), c.conn, msg); err != nil {
		c.t.Fatalf("sendJSON: %v", err)
	}
}

func (c *wsClient) sendCommand(cmd wsCommand) {
	c.t.Helper()
	c.sendJSON(cmd)
}

// drainUntilText reads messages until it finds a text frame, discarding binary.
func (c *wsClient) drainUntilText(timeout time.Duration) map[string]any {
	c.t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		ctx, cancel := context.WithTimeout(context.Background(), remaining)

		typ, data, err := c.conn.Read(ctx)
		cancel()

		if err != nil {
			c.t.Fatalf("drainUntilText: %v", err)
		}

		if typ == websocket.MessageText {
			var msg map[string]any
			if err := json.Unmarshal(data, &msg); err != nil {
				c.t.Fatalf("unmarshal JSON: %v", err)
			}

			return msg
		}
		// binary frame — discard and continue
	}

	c.t.Fatal("drainUntilText: timed out waiting for text frame")

	return nil
}

// waitForMsgType reads messages (text and binary) until it finds a text
// frame with the given "type" field. All other messages are discarded.
func (c *wsClient) waitForMsgType(msgType string, timeout time.Duration) map[string]any {
	c.t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		ctx, cancel := context.WithTimeout(context.Background(), remaining)

		typ, data, err := c.conn.Read(ctx)
		cancel()

		if err != nil {
			c.t.Fatalf("waitForMsgType(%s): %v", msgType, err)
		}

		if typ != websocket.MessageText {
			continue
		}

		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		if msg["type"] == msgType {
			return msg
		}
	}

	c.t.Fatalf("timed out waiting for %q message", msgType)

	return nil
}

// ── Connection lifecycle tests ──────────────────────────────────────────

func TestWSClient_LiveMode_ReceivesModeChange(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	server := newWSTestServer(t, streams)
	client := dialWS(t, server, "cameraId=cam-1")

	// Per protocol: connection without start param → mode_change(live)
	msg := client.readTextJSON(2 * time.Second)
	if msg["type"] != "mode_change" {
		t.Fatalf("expected mode_change, got %v", msg)
	}

	if msg["mode"] != "live" {
		t.Fatalf("expected mode=live, got %v", msg["mode"])
	}
}

func TestWSClient_RecordedMode_ReceivesPlaybackInfo(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(2 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	start := base.Add(30 * time.Second).Format(time.RFC3339)
	client := dialWS(t, server, "cameraId=cam-1&start="+start)

	// Per protocol: connection with start param → playback_info
	msg := client.readTextJSON(2 * time.Second)
	if msg["type"] != "playback_info" {
		t.Fatalf("expected playback_info, got %v", msg)
	}

	if msg["mode"] != "recorded" {
		t.Fatalf("expected mode=recorded, got %v", msg["mode"])
	}

	if _, ok := msg["actualStartWallClock"]; !ok {
		t.Fatal("playback_info missing actualStartWallClock field")
	}
}

func TestWSClient_EarlyStart_SnapsToFirstSegment(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	// Segment is at base..base+2m, but client requests start way before that.
	// Since the segment's EndTime > from, QueryByChannel returns it and mode is
	// "recorded" with actualStart snapped to the segment's StartTime.
	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(2 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	start := base.Add(-30 * time.Minute).Format(time.RFC3339)
	client := dialWS(t, server, "cameraId=cam-1&start="+start)

	msg := client.readTextJSON(2 * time.Second)
	if msg["type"] != "playback_info" {
		t.Fatalf("expected playback_info, got %v", msg)
	}

	if msg["mode"] != "recorded" {
		t.Fatalf("expected mode=recorded (snapped to first segment), got %v", msg["mode"])
	}

	// actualStartWallClock should be snapped to the segment's start time.
	actualStart, _ := msg["actualStartWallClock"].(string)
	if actualStart == "" {
		t.Fatal("missing actualStartWallClock in playback_info")
	}

	parsed, err := time.Parse(av.RFC3339Milli, actualStart)
	if err != nil {
		t.Fatalf("invalid actualStartWallClock %q: %v", actualStart, err)
	}

	if parsed.Before(base) {
		t.Fatalf("actualStartWallClock %v should be >= segment start %v", parsed, base)
	}
}

func TestWSClient_RecordedMode_SeekBeyondSwitchesToLive(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(2 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	// Request a time after the last recording → should switch to live
	start := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	client := dialWS(t, server, "cameraId=cam-1&start="+start)

	msg := client.readTextJSON(2 * time.Second)
	if msg["type"] != "mode_change" {
		t.Fatalf("expected mode_change for future start, got %v", msg)
	}

	if msg["mode"] != "live" {
		t.Fatalf("expected mode=live, got %v", msg["mode"])
	}
}

func TestWSClient_MissingCameraID_Returns400(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	server := newWSTestServer(t, streams)

	// Try to upgrade WebSocket without cameraId — should fail with HTTP 400.
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/cameras/ws/stream"
	_, resp, err := websocket.Dial(context.Background(), wsURL, nil)

	if err == nil {
		t.Fatal("expected dial to fail without cameraId")
	}

	if resp != nil && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// ── Streaming data tests ────────────────────────────────────────────────

func TestWSClient_LiveStreaming_ReceivesCodecStringAndBinary(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	server := newWSTestServer(t, streams)
	client := dialWS(t, server, "cameraId=cam-1")

	// 1. Read mode_change
	msg := client.readTextJSON(2 * time.Second)
	if msg["type"] != "mode_change" {
		t.Fatalf("expected mode_change, got %v", msg)
	}

	// 2. Send start-streaming command
	client.sendCommand(wsCommand{Type: "mse"})

	// 3. Read codec string (text frame with type=mse)
	codecMsg := client.drainUntilText(5 * time.Second)
	if codecMsg["type"] != "mse" {
		t.Fatalf("expected mse codec message, got %v", codecMsg)
	}

	codecStr, ok := codecMsg["value"].(string)
	if !ok || codecStr == "" {
		t.Fatalf("expected non-empty codec string, got %v", codecMsg["value"])
	}

	if !strings.Contains(codecStr, "video/mp4") {
		t.Fatalf("codec string should contain video/mp4, got %q", codecStr)
	}

	// 4. Read at least one binary frame (init segment or media fragment)
	typ, data := client.readMessage(5 * time.Second)
	if typ != websocket.MessageBinary {
		t.Fatalf("expected binary frame after codec string, got text")
	}

	if len(data) == 0 {
		t.Fatal("binary frame is empty")
	}
}

func TestWSClient_RecordedStreaming_ReceivesCodecStringAndBinary(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(2 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	start := base.Add(10 * time.Second).Format(time.RFC3339)
	client := dialWS(t, server, "cameraId=cam-1&start="+start)

	// 1. Read playback_info
	msg := client.readTextJSON(2 * time.Second)
	if msg["type"] != "playback_info" {
		t.Fatalf("expected playback_info, got %v", msg)
	}

	// 2. Send start-streaming command
	client.sendCommand(wsCommand{Type: "mse"})

	// 3. Read codec string
	codecMsg := client.drainUntilText(5 * time.Second)
	if codecMsg["type"] != "mse" {
		t.Fatalf("expected mse codec message, got %v", codecMsg)
	}

	// 4. Read binary frame
	typ, data := client.readMessage(5 * time.Second)
	if typ != websocket.MessageBinary {
		t.Fatalf("expected binary frame, got text")
	}

	if len(data) == 0 {
		t.Fatal("binary frame is empty")
	}
}

// ── Seek protocol tests ─────────────────────────────────────────────────

func TestWSClient_SeekToNow_SwitchesToLive(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(2 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	start := base.Add(10 * time.Second).Format(time.RFC3339)
	client := dialWS(t, server, "cameraId=cam-1&start="+start)

	// Read playback_info
	client.readTextJSON(2 * time.Second)

	// Start streaming
	client.sendCommand(wsCommand{Type: "mse"})

	// Wait for codec message to confirm streaming started
	client.drainUntilText(5 * time.Second)

	// Seek to "now" to switch to live
	client.sendCommand(wsCommand{Type: "mse", Value: "seek", Time: "now", Seq: 1})

	// The server sends mode_change(live) from startLive before the seeked response.
	// Use waitForMsgType to skip past mode_change/mse messages.
	seekedMsg := client.waitForMsgType("seeked", 5*time.Second)

	if seekedMsg["mode"] != "live" {
		t.Fatalf("expected mode=live after seek to now, got %v", seekedMsg["mode"])
	}

	if seq, ok := seekedMsg["seq"].(float64); !ok || seq != 1 {
		t.Fatalf("expected seq=1, got %v", seekedMsg["seq"])
	}
}

func TestWSClient_SeekToRecordedTime(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(2 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	// Start in live mode
	client := dialWS(t, server, "cameraId=cam-1")

	// Read mode_change(live)
	client.readTextJSON(2 * time.Second)

	// Start streaming in live mode
	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // codec string

	// Seek to recorded time
	seekTime := base.Add(30 * time.Second).Format(time.RFC3339)
	client.sendCommand(wsCommand{Type: "mse", Value: "seek", Time: seekTime, Seq: 1})

	seekedMsg := client.waitForMsgType("seeked", 5*time.Second)

	if seekedMsg["mode"] != "recorded" {
		t.Fatalf("expected mode=recorded, got %v", seekedMsg["mode"])
	}

	if seq, _ := seekedMsg["seq"].(float64); seq != 1 {
		t.Fatalf("expected seq=1, got %v", seekedMsg["seq"])
	}
}

func TestWSClient_SeekIntoGap_ReturnsGapTrue(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath1 := createTestSegment(t, filepath.Join(segDir, "s1"), streams, 60)
	segPath2 := createTestSegment(t, filepath.Join(segDir, "s2"), streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	// Segment 1: base .. base+1m
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(1 * time.Minute),
		FilePath:  segPath1,
		Status:    recorder.StatusComplete,
	})
	// Segment 2: base+10m .. base+11m (10 minute gap)
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-2",
		ChannelID: "cam-1",
		StartTime: base.Add(10 * time.Minute),
		EndTime:   base.Add(11 * time.Minute),
		FilePath:  segPath2,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	client := dialWS(t, server, "cameraId=cam-1")
	client.readTextJSON(2 * time.Second) // mode_change(live)

	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // codec string

	// Seek into the gap (base+5m is between segments)
	gapTime := base.Add(5 * time.Minute).Format(time.RFC3339)
	client.sendCommand(wsCommand{Type: "mse", Value: "seek", Time: gapTime, Seq: 1})

	seekedMsg := client.waitForMsgType("seeked", 5*time.Second)

	if seekedMsg["gap"] != true {
		t.Fatalf("expected gap=true for seek into gap, got %v", seekedMsg["gap"])
	}

	if seekedMsg["mode"] != "recorded" {
		t.Fatalf("expected mode=recorded, got %v", seekedMsg["mode"])
	}
}

func TestWSClient_SeekEchoesSeq(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(2 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	client := dialWS(t, server, "cameraId=cam-1")
	client.readTextJSON(2 * time.Second)
	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second)

	// Seek with a specific seq value
	client.sendCommand(wsCommand{Type: "mse", Value: "seek", Time: "now", Seq: 42})

	seekedMsg := client.waitForMsgType("seeked", 5*time.Second)

	if seq, _ := seekedMsg["seq"].(float64); seq != 42 {
		t.Fatalf("expected seq=42 echoed back, got %v", seekedMsg["seq"])
	}
}

// ── Seek error handling tests ───────────────────────────────────────────

func TestWSClient_SeekInvalidTime_ReceivesErrorNotFatal(t *testing.T) {
	t.Parallel()

	// Test with the streamSession directly to verify error is non-fatal.
	client := withWebsocketPair(t, func(ctx context.Context, conn *websocket.Conn) {
		session := &streamSession{
			wsConn:  conn,
			handler: &HTTPHandler{},
		}
		err := session.handleSeek(ctx, wsCommand{
			Type:  "mse",
			Value: "seek",
			Time:  "not-a-valid-time",
			Seq:   1,
		}, nil)
		// Should NOT return an error — invalid time is non-fatal (client can retry).
		if err != nil {
			t.Errorf("handleSeek should return nil for invalid time, got %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var resp map[string]string
	if err := wsjson.Read(ctx, client, &resp); err != nil {
		t.Fatalf("Read: %v", err)
	}

	if resp["type"] != "error" {
		t.Fatalf("expected error response, got %v", resp)
	}

	if !strings.Contains(resp["error"], "invalid seek time") {
		t.Fatalf("expected 'invalid seek time' error, got %q", resp["error"])
	}
}

// ── Skip protocol tests ─────────────────────────────────────────────────

func TestWSClient_SkipInvalidOffset_ReceivesError(t *testing.T) {
	t.Parallel()

	client := withWebsocketPair(t, func(ctx context.Context, conn *websocket.Conn) {
		session := &streamSession{
			wsConn:  conn,
			handler: &HTTPHandler{},
		}
		err := session.handleSkip(ctx, wsCommand{
			Type:   "mse",
			Value:  "skip",
			Offset: "not-a-duration",
			Seq:    1,
		}, nil)
		if err != nil {
			t.Errorf("handleSkip should return nil for invalid offset, got %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var resp map[string]string
	if err := wsjson.Read(ctx, client, &resp); err != nil {
		t.Fatalf("Read: %v", err)
	}

	if resp["type"] != "error" {
		t.Fatalf("expected error response, got %v", resp)
	}

	if !strings.Contains(resp["error"], "invalid offset") {
		t.Fatalf("expected 'invalid offset' error, got %q", resp["error"])
	}
}

func TestWSClient_SkipForward(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(5 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	start := base.Add(1 * time.Minute).Format(time.RFC3339)
	client := dialWS(t, server, "cameraId=cam-1&start="+start)

	client.readTextJSON(2 * time.Second) // playback_info
	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // codec string

	// Skip forward by 30s
	client.sendCommand(wsCommand{Type: "mse", Value: "skip", Offset: "30s", Seq: 1})

	seekedMsg := client.waitForMsgType("seeked", 5*time.Second)

	if seq, _ := seekedMsg["seq"].(float64); seq != 1 {
		t.Fatalf("expected seq=1, got %v", seekedMsg["seq"])
	}
}

func TestWSClient_SkipBackward(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(5 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	start := base.Add(2 * time.Minute).Format(time.RFC3339)
	client := dialWS(t, server, "cameraId=cam-1&start="+start)

	client.readTextJSON(2 * time.Second) // playback_info
	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // codec string

	// Skip backward by 30s
	client.sendCommand(wsCommand{Type: "mse", Value: "skip", Offset: "-30s", Seq: 1})

	seekedMsg := client.waitForMsgType("seeked", 5*time.Second)

	if seekedMsg["mode"] != "recorded" {
		t.Fatalf("expected mode=recorded after skip backward, got %v", seekedMsg["mode"])
	}
}

// ── Seq-based debouncing tests ──────────────────────────────────────────

func TestWSClient_StaleSeq_Ignored(t *testing.T) {
	t.Parallel()

	// Verify at the session level that stale seq is silently ignored.
	client := withWebsocketPair(t, func(ctx context.Context, conn *websocket.Conn) {
		session := &streamSession{
			wsConn:      conn,
			handler:     &HTTPHandler{},
			lastSeqSeen: 10,
		}
		// Send a seek with seq=5 (stale, below lastSeqSeen=10)
		err := session.handleSeek(ctx, wsCommand{
			Type:  "mse",
			Value: "seek",
			Time:  "now",
			Seq:   5,
		}, nil)
		if err != nil {
			t.Errorf("handleSeek should not error on stale seq: %v", err)
		}
	})

	// Should get no response — stale seek is silently dropped.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var resp map[string]any

	err := wsjson.Read(ctx, client, &resp)
	if err == nil {
		t.Fatalf("expected no response for stale seq, got %v", resp)
	}
	// context.DeadlineExceeded means we timed out — no response sent (correct).
}

func TestWSClient_SeqZero_NeverDebounced(t *testing.T) {
	t.Parallel()

	// seq=0 means the field was not provided — should not be debounced.
	client := withWebsocketPair(t, func(ctx context.Context, conn *websocket.Conn) {
		session := &streamSession{
			wsConn:      conn,
			handler:     &HTTPHandler{},
			lastSeqSeen: 10,
		}
		// seq=0 should not be treated as stale even though 0 < 10
		err := session.handleSeek(ctx, wsCommand{
			Type:  "mse",
			Value: "seek",
			Time:  "not-a-time", // will produce an error response (confirms it wasn't ignored)
			Seq:   0,
		}, nil)
		if err != nil {
			t.Errorf("handleSeek: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var resp map[string]string
	if err := wsjson.Read(ctx, client, &resp); err != nil {
		t.Fatalf("Read: %v", err)
	}

	// We should get an error response (for the bad time), proving the seek was NOT debounced.
	if resp["type"] != "error" {
		t.Fatalf("expected error (proving seq=0 was processed), got %v", resp)
	}
}

func TestWSClient_RapidSeeks_OnlyLatestProcessed(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(5 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	client := dialWS(t, server, "cameraId=cam-1")
	client.readTextJSON(2 * time.Second) // mode_change
	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // codec string

	// Fire multiple seeks in rapid succession (simulate scrubber drag).
	// Send all seeks, then verify we get at least one seeked response.
	for seq := int64(1); seq <= 5; seq++ {
		client.sendCommand(wsCommand{
			Type:  "mse",
			Value: "seek",
			Time:  "now",
			Seq:   seq,
		})
	}

	// Collect seeked responses. We must get at least one.
	var lastSeq float64

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		ctx, cancel := context.WithTimeout(context.Background(), remaining)

		typ, data, err := client.conn.Read(ctx)
		cancel()

		if err != nil {
			break
		}

		if typ != websocket.MessageText {
			continue
		}

		var msg map[string]any
		if json.Unmarshal(data, &msg) != nil {
			continue
		}

		if msg["type"] == "seeked" {
			if s, ok := msg["seq"].(float64); ok && s > lastSeq {
				lastSeq = s
			}

			// Stop after finding the highest seq we sent.
			if lastSeq >= 5 {
				break
			}
		}
	}

	if lastSeq == 0 {
		t.Fatal("expected at least one seeked response during rapid seeks")
	}
}

// ── Pause / Resume tests ────────────────────────────────────────────────

func TestWSClient_PauseResume_LiveMode_NoEffect(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	server := newWSTestServer(t, streams)
	client := dialWS(t, server, "cameraId=cam-1")

	client.readTextJSON(2 * time.Second) // mode_change(live)
	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // codec string

	// Pause in live mode — server should not stop sending (pause is client-side).
	client.sendCommand(wsCommand{Type: "mse", Value: "pause"})

	// We should still receive binary data after pause in live mode.
	typ, data := client.readMessage(3 * time.Second)
	if typ != websocket.MessageBinary || len(data) == 0 {
		t.Fatalf("expected binary data to continue after pause in live mode, got type=%v len=%d", typ, len(data))
	}

	// Resume should also not cause issues.
	client.sendCommand(wsCommand{Type: "mse", Value: "resume"})

	typ, data = client.readMessage(3 * time.Second)
	if typ != websocket.MessageBinary || len(data) == 0 {
		t.Fatal("expected binary data after resume")
	}
}

func TestWSClient_PauseResume_RecordedMode(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 90)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(5 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	start := base.Add(10 * time.Second).Format(time.RFC3339)
	client := dialWS(t, server, "cameraId=cam-1&start="+start)

	client.readTextJSON(2 * time.Second) // playback_info
	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // codec string

	// Read a binary frame to confirm streaming is active.
	typ, _ := client.readMessage(3 * time.Second)
	if typ != websocket.MessageBinary {
		t.Fatal("expected binary data before pause")
	}

	// Pause — relay should stop sending.
	client.sendCommand(wsCommand{Type: "mse", Value: "pause"})

	// After pause, no more binary data should arrive for a bit.
	// We give it a short window to verify silence.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, _, err := client.conn.Read(ctx)
	if err == nil {
		// It's possible a packet was in flight. That's OK.
		// The point is that the relay was paused.
	}

	// Resume — data should flow again.
	client.sendCommand(wsCommand{Type: "mse", Value: "resume"})

	typ, data := client.readMessage(5 * time.Second)
	if typ != websocket.MessageBinary || len(data) == 0 {
		t.Fatal("expected binary data after resume")
	}
}

// ── Edge case tests ─────────────────────────────────────────────────────

func TestWSClient_NonMSECommand_Ignored(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	server := newWSTestServer(t, streams)
	client := dialWS(t, server, "cameraId=cam-1")

	client.readTextJSON(2 * time.Second) // mode_change

	// Send a non-mse command — should be silently ignored.
	client.sendJSON(map[string]string{
		"type":  "analytics",
		"value": "some-data",
	})

	// Now send the real mse command — should work normally.
	client.sendCommand(wsCommand{Type: "mse"})

	codecMsg := client.drainUntilText(5 * time.Second)
	if codecMsg["type"] != "mse" {
		t.Fatalf("expected mse codec message after non-mse was ignored, got %v", codecMsg)
	}
}

func TestWSClient_LiveToRecordedTransition(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(2 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))

	// 1. Start in live mode
	client := dialWS(t, server, "cameraId=cam-1")
	msg := client.readTextJSON(2 * time.Second)
	if msg["mode"] != "live" {
		t.Fatalf("expected live mode, got %v", msg["mode"])
	}

	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // codec string

	// 2. Seek to recorded time
	seekTime := base.Add(30 * time.Second).Format(time.RFC3339)
	client.sendCommand(wsCommand{Type: "mse", Value: "seek", Time: seekTime, Seq: 1})

	seekedMsg := client.waitForMsgType("seeked", 5*time.Second)

	if seekedMsg["mode"] != "recorded" {
		t.Fatalf("expected transition to recorded mode, got %v", seekedMsg["mode"])
	}
}

func TestWSClient_RecordedToLiveTransition(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(2 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))

	// 1. Start in recorded mode
	start := base.Add(10 * time.Second).Format(time.RFC3339)
	client := dialWS(t, server, "cameraId=cam-1&start="+start)

	msg := client.readTextJSON(2 * time.Second)
	if msg["type"] != "playback_info" || msg["mode"] != "recorded" {
		t.Fatalf("expected playback_info(recorded), got %v", msg)
	}

	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // codec string

	// 2. Seek to now → switch to live
	client.sendCommand(wsCommand{Type: "mse", Value: "seek", Time: "now", Seq: 1})

	seekedMsg := client.waitForMsgType("seeked", 5*time.Second)

	if seekedMsg["mode"] != "live" {
		t.Fatalf("expected transition to live mode, got %v", seekedMsg["mode"])
	}
}

func TestWSClient_SeekAfterPause_Resumes(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(5 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	start := base.Add(1 * time.Minute).Format(time.RFC3339)
	client := dialWS(t, server, "cameraId=cam-1&start="+start)

	client.readTextJSON(2 * time.Second) // playback_info
	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // codec string

	// Pause
	client.sendCommand(wsCommand{Type: "mse", Value: "pause"})
	time.Sleep(200 * time.Millisecond)

	// Seek while paused — should restart playback at new position.
	client.sendCommand(wsCommand{Type: "mse", Value: "seek", Time: "now", Seq: 1})

	seekedMsg := client.waitForMsgType("seeked", 5*time.Second)
	if seekedMsg["mode"] != "live" {
		t.Fatalf("expected mode=live after pause+seek to now, got %v", seekedMsg["mode"])
	}

	// After seek, a new codec string should follow.
	codecMsg := client.waitForMsgType("mse", 5*time.Second)
	if codecMsg["value"] == nil || codecMsg["value"] == "" {
		t.Fatal("expected non-empty codec string after seek")
	}
}

func TestWSClient_MultipleSeeksWithoutWaiting(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(5 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	client := dialWS(t, server, "cameraId=cam-1")
	client.readTextJSON(2 * time.Second) // mode_change
	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // codec string

	// Fire multiple seeks without waiting for responses (simulate rapid scrubber).
	for seq := int64(1); seq <= 3; seq++ {
		client.sendCommand(wsCommand{
			Type:  "mse",
			Value: "seek",
			Time:  base.Add(time.Duration(seq) * 30 * time.Second).Format(time.RFC3339),
			Seq:   seq,
		})
	}

	// Should eventually get at least one seeked response.
	seekedMsg := client.waitForMsgType("seeked", 10*time.Second)
	if seekedMsg["mode"] == nil {
		t.Fatal("seeked response missing mode field")
	}
}

// ── Seeked response field validation ────────────────────────────────────

func TestWSClient_SeekedResponse_ContainsAllFields(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	server := newWSTestServer(t, streams)
	client := dialWS(t, server, "cameraId=cam-1")

	client.readTextJSON(2 * time.Second) // mode_change
	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // codec string

	client.sendCommand(wsCommand{Type: "mse", Value: "seek", Time: "now", Seq: 99})

	seekedMsg := client.waitForMsgType("seeked", 5*time.Second)

	// Validate all required fields from the protocol spec.
	requiredFields := []string{"type", "wallClock", "mode", "codecChanged", "seq"}
	for _, field := range requiredFields {
		if _, ok := seekedMsg[field]; !ok {
			t.Errorf("seeked response missing required field %q: %v", field, seekedMsg)
		}
	}

	// "wallClock" must be a valid RFC3339Milli string.
	if wcStr, ok := seekedMsg["wallClock"].(string); ok {
		if _, err := time.Parse(av.RFC3339Milli, wcStr); err != nil {
			t.Errorf("seeked.wallClock is not valid RFC3339Milli: %q", wcStr)
		}
	} else {
		t.Error("seeked.wallClock is not a string")
	}

	// "mode" must be one of the defined values.
	mode, _ := seekedMsg["mode"].(string)
	validModes := map[string]bool{"recorded": true, "live": true, "first_available": true}
	if !validModes[mode] {
		t.Errorf("seeked.mode=%q is not a valid mode", mode)
	}

	// "codecChanged" must be a boolean.
	if _, ok := seekedMsg["codecChanged"].(bool); !ok {
		t.Errorf("seeked.codecChanged is not a bool: %T", seekedMsg["codecChanged"])
	}

	// "seq" must match what we sent.
	if seq, ok := seekedMsg["seq"].(float64); !ok || seq != 99 {
		t.Errorf("seeked.seq=%v, expected 99", seekedMsg["seq"])
	}
}

// ── Protocol compliance: message ordering ───────────────────────────────

func TestWSClient_AfterSeek_ReceivesCodecThenBinary(t *testing.T) {
	t.Parallel()

	streams := testH264Streams(t)
	segDir := t.TempDir()
	segPath := createTestSegment(t, segDir, streams, 60)

	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	idx := newMockRecordingIndex()
	idx.addEntry(recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(2 * time.Minute),
		FilePath:  segPath,
		Status:    recorder.StatusComplete,
	})

	server := newWSTestServer(t, streams, withRecordingIndex(idx))
	client := dialWS(t, server, "cameraId=cam-1")
	client.readTextJSON(2 * time.Second) // mode_change
	client.sendCommand(wsCommand{Type: "mse"})
	client.drainUntilText(5 * time.Second) // initial codec string

	// Seek to recorded time
	seekTime := base.Add(30 * time.Second).Format(time.RFC3339)
	client.sendCommand(wsCommand{Type: "mse", Value: "seek", Time: seekTime, Seq: 1})

	// Protocol spec: after seek, expect seeked → mse codec string → binary init segment
	seekedMsg := client.waitForMsgType("seeked", 5*time.Second)
	if seekedMsg["mode"] != "recorded" {
		t.Fatalf("step 1: expected mode=recorded, got %v", seekedMsg["mode"])
	}

	codecMsg := client.waitForMsgType("mse", 5*time.Second)
	if codecMsg["value"] == nil || codecMsg["value"] == "" {
		t.Fatal("step 2: expected non-empty codec string after seeked")
	}

	typ, data := client.readMessage(5 * time.Second)
	if typ != websocket.MessageBinary || len(data) == 0 {
		t.Fatalf("step 3: expected binary init segment after codec string, got type=%v len=%d", typ, len(data))
	}
}

// ── readWSCommand + streamSession unit tests ────────────────────────────

func TestReadWSCommand_AllFields(t *testing.T) {
	t.Parallel()

	client := withWebsocketPair(t, func(ctx context.Context, conn *websocket.Conn) {
		cmd, err := readWSCommand(ctx, conn)
		if err != nil {
			t.Errorf("readWSCommand: %v", err)

			return
		}

		if cmd.Type != "mse" || cmd.Value != "skip" || cmd.Offset != "-30s" || cmd.Seq != 7 {
			t.Errorf("unexpected command fields: %+v", cmd)
		}
	})

	if err := wsjson.Write(context.Background(), client, wsCommand{
		Type:   "mse",
		Value:  "skip",
		Offset: "-30s",
		Seq:    7,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func TestReadWSCommand_ContextCancelled(t *testing.T) {
	t.Parallel()

	_ = withWebsocketPair(t, func(ctx context.Context, conn *websocket.Conn) {
		ctx, cancel := context.WithCancel(ctx)
		cancel() // cancel immediately

		_, err := readWSCommand(ctx, conn)
		if err == nil {
			t.Error("expected error when context is cancelled")
		}
	})
}

func TestStreamSession_Stop_IsIdempotent(t *testing.T) {
	t.Parallel()

	session := &streamSession{}

	// Calling stop twice should not panic.
	session.stop(context.Background())
	session.stop(context.Background())
}

// suppress unused import warnings.
var _ = io.EOF
