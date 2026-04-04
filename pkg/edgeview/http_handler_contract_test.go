package edgeview

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog/log"
)

func newContractHandler(authToken string) http.Handler {
	svc := NewService(log.Logger, fakeRelayHub{}, nil, nil)

	return NewHTTPHandler(svc, log.Logger, authToken).Router()
}

func TestAuthMiddleware_AllowsHealthWithoutAuth(t *testing.T) {
	t.Parallel()

	handler := newContractHandler("secret")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected /healthz to bypass auth, got %d", rec.Code)
	}
}

func TestAuthMiddleware_RejectsUnauthorizedCameraAPI(t *testing.T) {
	t.Parallel()

	handler := newContractHandler("secret")
	req := httptest.NewRequest(http.MethodGet, "/api/cameras", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected /api/cameras to require auth, got %d", rec.Code)
	}
}

func TestRouter_RejectsRemovedLegacyHTTPAlias(t *testing.T) {
	t.Parallel()

	handler := newContractHandler("")
	req := httptest.NewRequest(http.MethodGet, "/api/cameras/cam-1/live", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected removed legacy live alias to return 404, got %d", rec.Code)
	}
}

func TestRouter_RejectsRemovedLegacyPlaybackAlias(t *testing.T) {
	t.Parallel()

	handler := newContractHandler("")
	req := httptest.NewRequest(http.MethodGet, "/api/cameras/cam-1/playback", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected removed legacy playback alias to return 404, got %d", rec.Code)
	}
}

func TestRouter_RejectsRemovedLegacyRecordedAlias(t *testing.T) {
	t.Parallel()

	handler := newContractHandler("")
	req := httptest.NewRequest(http.MethodGet, "/api/cameras/cam-1/recorded", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected removed legacy recorded alias to return 404, got %d", rec.Code)
	}
}

func TestRouter_RejectsRemovedLegacyWSAlias(t *testing.T) {
	t.Parallel()

	handler := newContractHandler("")
	req := httptest.NewRequest(http.MethodGet, "/api/cameras/ws/live", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected removed legacy ws alias to return 404, got %d", rec.Code)
	}
}

func TestWSStream_RequiresCameraID(t *testing.T) {
	t.Parallel()

	handler := newContractHandler("")
	req := httptest.NewRequest(http.MethodGet, "/api/cameras/ws/stream", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected missing cameraId to return 400, got %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestParsePlaybackStart_InvalidRFC3339(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/cameras/cam-1/stream?start=not-a-time", nil)

	_, err := parsePlaybackStart(req)
	if err == nil {
		t.Fatal("expected invalid start timestamp to return an error")
	}
}

func TestParseOptionalTimeRange_EndBeforeStart(t *testing.T) {
	t.Parallel()

	_, _, err := parseOptionalTimeRange("2026-04-04T12:00:00Z", "2026-04-04T11:00:00Z")
	if err == nil {
		t.Fatal("expected end-before-start to return an error")
	}
}
