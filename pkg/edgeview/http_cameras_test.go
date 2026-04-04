package edgeview

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc/pkg/channel"
)

type fakeRelayHub struct{}

func (fakeRelayHub) GetRelayStats(context.Context) []av.RelayStats { return nil }

func (fakeRelayHub) GetRelayStatsByID(context.Context, string) (av.RelayStats, bool) {
	return av.RelayStats{}, false
}

func (fakeRelayHub) ListRelayIDs(context.Context) []string { return nil }

func (fakeRelayHub) GetActiveRelayCount(context.Context) int { return 0 }

func (fakeRelayHub) Consume(context.Context, string, av.ConsumeOptions) (av.ConsumerHandle, error) {
	return nil, nil
}

func (fakeRelayHub) PauseRelay(context.Context, string) error { return nil }

func (fakeRelayHub) ResumeRelay(context.Context, string) error { return nil }

func (fakeRelayHub) Start(context.Context) error { return nil }

func (fakeRelayHub) SignalStop() bool { return true }

func (fakeRelayHub) WaitStop() error { return nil }

func (fakeRelayHub) Stop() error { return nil }

type fakeChannelWriter struct {
	channels map[string]channel.Channel
}

func newFakeChannelWriter() *fakeChannelWriter {
	return &fakeChannelWriter{channels: make(map[string]channel.Channel)}
}

func (f *fakeChannelWriter) GetChannel(_ context.Context, id string) (channel.Channel, error) {
	ch, ok := f.channels[id]
	if !ok {
		return channel.Channel{}, channel.ErrChannelNotFound
	}

	return ch, nil
}

func (f *fakeChannelWriter) ListChannels(context.Context) ([]channel.Channel, error) {
	out := make([]channel.Channel, 0, len(f.channels))
	for _, ch := range f.channels {
		out = append(out, ch)
	}

	return out, nil
}

func (f *fakeChannelWriter) Close() error { return nil }

func (f *fakeChannelWriter) SaveChannel(_ context.Context, ch channel.Channel) error {
	f.channels[ch.ID] = ch

	return nil
}

func (f *fakeChannelWriter) DeleteChannel(_ context.Context, id string) error {
	if _, ok := f.channels[id]; !ok {
		return channel.ErrChannelNotFound
	}

	delete(f.channels, id)

	return nil
}

func newCameraCSVHandler(t *testing.T) http.Handler {
	t.Helper()

	writer := newFakeChannelWriter()
	svc := NewService(log.Logger, fakeRelayHub{}, nil, nil, WithChannelWriter(writer))

	return NewHTTPHandler(svc, log.Logger, "").Router()
}

func TestImportCSV_RejectsNameOnlyLegacyHeader(t *testing.T) {
	t.Parallel()

	handler := newCameraCSVHandler(t)
	body := "name,rtsp_main\ncam-1,rtsp://example/main\n"

	req := httptest.NewRequest(http.MethodPost, "/api/cameras/import.csv", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected legacy name-only CSV to be rejected with 400, got %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestImportCSV_AcceptsIDHeader(t *testing.T) {
	t.Parallel()

	handler := newCameraCSVHandler(t)
	body := "id,name,rtsp_main\ncam-1,Front Door,rtsp://example/main\n"

	req := httptest.NewRequest(http.MethodPost, "/api/cameras/import.csv", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected id-based CSV to be accepted, got %d body=%q", rec.Code, rec.Body.String())
	}
}
