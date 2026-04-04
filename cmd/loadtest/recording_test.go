package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func readCapturedStdout(r *os.File) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return nil, err
	}

	return &buf, nil
}

func TestRunRecordingTest_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/cameras/cam-1/recordings") {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}

		_, _ = io.WriteString(w, `[
			{"start":"2026-04-04T11:00:00Z","end":"2026-04-04T11:30:00Z","duration_ms":1800000},
			{"start":"2026-04-04T11:30:01Z","end":"2026-04-04T12:00:00Z","duration_ms":1799000}
		]`)
	}))
	defer server.Close()

	prevTarget := flagTarget
	flagTarget = server.URL
	defer func() { flagTarget = prevTarget }()

	prevJSON := flagOutputJSON
	flagOutputJSON = true
	defer func() { flagOutputJSON = prevJSON }()

	prevStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	if err := runRecordingTest("cam-1", time.Hour); err != nil {
		t.Fatalf("runRecordingTest: %v", err)
	}

	_ = w.Close()
	os.Stdout = prevStdout
	buf, err := readCapturedStdout(r)
	if err != nil {
		t.Fatalf("readCapturedStdout: %v", err)
	}
	_ = r.Close()

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if result["camera_id"] != "cam-1" {
		t.Fatalf("expected camera_id cam-1, got %v", result["camera_id"])
	}

	if result["gaps"].(float64) != 0 {
		t.Fatalf("expected no gaps, got %v", result["gaps"])
	}
}

func TestRunRecordingTest_ThresholdOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[
			{"start":"2026-04-04T11:00:00Z","end":"2026-04-04T11:59:56.400Z","duration_ms":3596400}
		]`)
	}))
	defer server.Close()

	prevTarget := flagTarget
	flagTarget = server.URL
	defer func() { flagTarget = prevTarget }()

	prevJSON := flagOutputJSON
	flagOutputJSON = false
	defer func() { flagOutputJSON = prevJSON }()

	prevStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	if err := runRecordingTest("cam-1", time.Hour); err != nil {
		t.Fatalf("runRecordingTest: %v", err)
	}

	_ = w.Close()
	os.Stdout = prevStdout
	buf, err := readCapturedStdout(r)
	if err != nil {
		t.Fatalf("readCapturedStdout: %v", err)
	}
	_ = r.Close()

	if !strings.Contains(buf.String(), "PASS") {
		t.Fatalf("expected PASS threshold output, got %q", buf.String())
	}
}

func TestRunRecordingTest_InvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `not-json`)
	}))
	defer server.Close()

	prevTarget := flagTarget
	flagTarget = server.URL
	defer func() { flagTarget = prevTarget }()

	err := runRecordingTest("cam-1", time.Hour)
	if err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Fatalf("expected parse response error, got %v", err)
	}
}

func TestAPIAndTimelineOnce_StatusErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	if _, err := apiOnce(context.Background(), server.URL); err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("expected apiOnce status error, got %v", err)
	}

	if _, err := timelineOnce(context.Background(), server.URL); err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("expected timelineOnce status error, got %v", err)
	}
}

func TestRunStreamAndPlaybackTest_RequestPaths(t *testing.T) {
	type call struct {
		path string
	}

	var calls []call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, call{path: r.URL.RequestURI()})
		_, _ = io.WriteString(w, "test-payload")
	}))
	defer server.Close()

	prevTarget := flagTarget
	prevConcurrency := flagConcurrency
	prevDuration := flagDuration
	flagTarget = server.URL
	flagConcurrency = 1
	flagDuration = 10 * time.Millisecond
	defer func() {
		flagTarget = prevTarget
		flagConcurrency = prevConcurrency
		flagDuration = prevDuration
	}()

	if err := runStreamTest("cam-1"); err != nil {
		t.Fatalf("runStreamTest: %v", err)
	}

	if err := runPlaybackTest("cam-1", 5*time.Minute); err != nil {
		t.Fatalf("runPlaybackTest: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(calls))
	}

	if calls[0].path != "/api/cameras/cam-1/stream" {
		t.Fatalf("unexpected stream path %q", calls[0].path)
	}

	if !strings.HasPrefix(calls[1].path, "/api/cameras/cam-1/stream?start=") {
		t.Fatalf("unexpected playback path %q", calls[1].path)
	}
}
