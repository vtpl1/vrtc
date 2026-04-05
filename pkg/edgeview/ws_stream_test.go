package edgeview

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type wsTestHandle struct {
	id       string
	closed   atomic.Int32
	closeErr error
}

func (h *wsTestHandle) ID() string { return h.id }

func (h *wsTestHandle) Close(context.Context) error {
	h.closed.Add(1)

	return h.closeErr
}

func withWebsocketPair(
	t *testing.T,
	serverFn func(context.Context, *websocket.Conn),
) *websocket.Conn {
	t.Helper()

	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			t.Errorf("Accept: %v", err)

			return
		}

		defer close(done)
		defer conn.CloseNow()

		serverFn(r.Context(), conn)
	}))
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		<-done
	})

	return conn
}

func TestReadWSCommand_DecodesJSON(t *testing.T) {
	t.Parallel()

	client := withWebsocketPair(t, func(ctx context.Context, conn *websocket.Conn) {
		cmd, err := readWSCommand(ctx, conn)
		if err != nil {
			t.Errorf("readWSCommand: %v", err)

			return
		}

		if cmd.Value != "seek" || cmd.Time != "now" || cmd.Seq != 9 {
			t.Errorf("unexpected command: %+v", cmd)
		}
	})

	if err := wsjson.Write(context.Background(), client, wsCommand{
		Type:  "mse",
		Value: "seek",
		Time:  "now",
		Seq:   9,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func TestHandleSeek_InvalidTimeReturnsProtocolError(t *testing.T) {
	t.Parallel()

	client := withWebsocketPair(t, func(ctx context.Context, conn *websocket.Conn) {
		session := &streamSession{wsConn: conn}
		if err := session.handleSeek(ctx, wsCommand{Type: "mse", Value: "seek", Time: "not-a-time"}, nil); err != nil {
			t.Errorf("handleSeek: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var resp map[string]string
	if err := wsjson.Read(ctx, client, &resp); err != nil {
		t.Fatalf("Read: %v", err)
	}

	if resp["type"] != "error" || !strings.Contains(resp["error"], "invalid seek time") {
		t.Fatalf("unexpected error response: %+v", resp)
	}
}

func TestHandleSeek_StaleSeqIgnored(t *testing.T) {
	t.Parallel()

	client := withWebsocketPair(t, func(ctx context.Context, conn *websocket.Conn) {
		session := &streamSession{
			wsConn:      conn,
			lastSeqSeen: 10,
		}
		if err := session.handleSeek(ctx, wsCommand{Type: "mse", Value: "seek", Time: "now", Seq: 9}, nil); err != nil {
			t.Errorf("handleSeek: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	var resp map[string]any
	err := wsjson.Read(ctx, client, &resp)
	if err == nil {
		t.Fatalf("expected no response for stale seek, got %+v", resp)
	}

	if websocket.CloseStatus(err) != -1 && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected read timeout for ignored stale seek, got %v", err)
	}
}

func TestStreamSession_StartLiveSendsModeChange(t *testing.T) {
	t.Parallel()

	client := withWebsocketPair(t, func(ctx context.Context, conn *websocket.Conn) {
		session := &streamSession{wsConn: conn}
		if err := session.startLive(ctx); err != nil {
			t.Errorf("startLive: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var resp map[string]any
	if err := wsjson.Read(ctx, client, &resp); err != nil {
		t.Fatalf("Read: %v", err)
	}

	if resp["type"] != "mode_change" || resp["mode"] != "live" {
		t.Fatalf("unexpected mode-change response: %+v", resp)
	}
}

func TestStreamSession_Stop_ClosesHandlesAndUntracks(t *testing.T) {
	t.Parallel()

	var untracked atomic.Int32
	consumer := &wsTestHandle{id: "playback"}
	liveHandle := &wsTestHandle{id: "live"}

	session := &streamSession{
		consumer:   consumer,
		liveHandle: liveHandle,
		untrack: func() {
			untracked.Add(1)
		},
		liveMode: true,
	}

	session.stop(context.Background())

	if consumer.closed.Load() != 1 {
		t.Fatalf("expected playback consumer to be closed once, got %d", consumer.closed.Load())
	}

	if liveHandle.closed.Load() != 1 {
		t.Fatalf("expected live handle to be closed once, got %d", liveHandle.closed.Load())
	}

	if untracked.Load() != 1 {
		t.Fatalf("expected untrack to be called once, got %d", untracked.Load())
	}
}
