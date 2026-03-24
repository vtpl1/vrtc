//go:build cgo

// Integration tests for the avgrabber package against live RTSP cameras.
// All cameras in testCameras must be reachable on the network.
// Run with:  go test -v -count=1 -run RTSP ./internal/avgrabber/
// Skip in CI by passing -short: tests call t.Skip when testing.Short() is true.
package avgrabber_test

import (
	"context"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/internal/avgrabber"
	"github.com/vtpl1/vrtc/pkg/av"
)

const (
	testRTSPURL      = "rtsp://192.168.10.35/media/video1"
	testRTSPUsername = "admin"
	testRTSPPassword = "AdmiN1234"

	// How many video frames to collect before declaring the stream healthy.
	minVideoFrames = 30
	// How long to wait for the stream to become healthy.
	streamTimeout = 20 * time.Second
)

type cameraFixture struct {
	name string
	cfg  avgrabber.Config
}

// testCameras is the list of live cameras exercised by every table-driven test.
var testCameras = []cameraFixture{
	{
		name: "H265-192.168.10.35",
		cfg: avgrabber.Config{
			URL:      "rtsp://192.168.10.35/media/video1",
			Username: "admin",
			Password: "AdmiN1234",
			Protocol: avgrabber.ProtoTCP,
			Audio:    true,
		},
	},
	{
		name: "172.16.0.158",
		cfg: avgrabber.Config{
			URL:      "rtsp://172.16.0.158/LiveMedia/ch1/Media1",
			Username: "admin",
			Password: "AdmiN1234",
			Protocol: avgrabber.ProtoTCP,
			Audio:    true,
		},
	},
	{
		name: "Axis-192.168.10.84",
		cfg: avgrabber.Config{
			URL:      "rtsp://192.168.10.84:554/axis-media/media.amp",
			Username: "admin",
			Password: "AdmiN1234",
			Protocol: avgrabber.ProtoTCP,
			Audio:    true,
		},
	},
}

// rtspConfig returns the config for the primary test camera (used by MSE tests).
func rtspConfig() avgrabber.Config {
	return testCameras[0].cfg
}

// ── Session-level tests ────────────────────────────────────────────────────────

// TestSession_RTSP_Open verifies that the C library can open and close each
// RTSP session without leaking resources.
func TestSession_RTSP_Open(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping RTSP integration test in short mode")
	}

	avgrabber.Init()
	t.Cleanup(avgrabber.Deinit)

	for _, cam := range testCameras {
		t.Run(cam.name, func(t *testing.T) {
			s, err := avgrabber.Open(cam.cfg)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			if err := s.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

// TestSession_RTSP_ReceivesFrames verifies that frames are delivered and that
// at least one PARAM_SET and one KEY frame arrive within the timeout.
func TestSession_RTSP_ReceivesFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping RTSP integration test in short mode")
	}

	avgrabber.Init()
	t.Cleanup(avgrabber.Deinit)

	for _, cam := range testCameras {
		t.Run(cam.name, func(t *testing.T) {
			s, err := avgrabber.Open(cam.cfg)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			t.Cleanup(func() { _ = s.Close() })

			deadline := time.Now().Add(streamTimeout)

			var (
				gotParamSet bool
				gotKey      bool
				videoFrames int
			)

			for time.Now().Before(deadline) && (!gotParamSet || !gotKey || videoFrames < minVideoFrames) {
				f, err := s.NextFrame(50)
				if err != nil {
					if avgrabber.IsNotReady(err) {
						continue
					}

					t.Fatalf("NextFrame: %v", err)
				}

				if f.MediaType == avgrabber.MediaVideo {
					switch f.FrameType {
					case avgrabber.FrameTypeParamSet:
						gotParamSet = true

						if len(f.Data) == 0 {
							t.Error("PARAM_SET frame has empty data")
						}
					case avgrabber.FrameTypeKey:
						gotKey = true
						videoFrames++

						if len(f.Data) == 0 {
							t.Error("KEY frame has empty data")
						}

						if f.PTSTicks == 0 && videoFrames > 1 {
							t.Error("KEY frame PTSTicks is 0")
						}
					case avgrabber.FrameTypeDelta:
						videoFrames++
					}
				}
			}

			if !gotParamSet {
				t.Error("no PARAM_SET frame received within timeout")
			}

			if !gotKey {
				t.Error("no KEY frame received within timeout")
			}

			if videoFrames < minVideoFrames {
				t.Errorf("only %d video frames received, want >=%d", videoFrames, minVideoFrames)
			}
		})
	}
}

// TestSession_RTSP_StreamInfo verifies that stream info is populated with
// plausible values after the first PARAM_SET.
func TestSession_RTSP_StreamInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping RTSP integration test in short mode")
	}

	avgrabber.Init()
	t.Cleanup(avgrabber.Deinit)

	for _, cam := range testCameras {
		t.Run(cam.name, func(t *testing.T) {
			s, err := avgrabber.Open(cam.cfg)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			t.Cleanup(func() { _ = s.Close() })

			deadline := time.Now().Add(streamTimeout)

			var info avgrabber.StreamInfo

			for time.Now().Before(deadline) {
				f, ferr := s.NextFrame(50)
				if ferr != nil {
					if avgrabber.IsNotReady(ferr) {
						continue
					}

					t.Fatalf("NextFrame: %v", ferr)
				}

				if f.MediaType == avgrabber.MediaVideo && f.FrameType == avgrabber.FrameTypeParamSet {
					info, err = s.GetStreamInfo()
					if err != nil {
						t.Fatalf("GetStreamInfo: %v", err)
					}

					break
				}
			}

			if info.Width == 0 || info.Height == 0 {
				t.Errorf("invalid resolution %dx%d", info.Width, info.Height)
			}

			if info.VideoClockRate == 0 {
				t.Error("VideoClockRate is 0")
			}

			knownVideoCodecs := map[uint8]string{
				avgrabber.CodecH264:  "H.264",
				avgrabber.CodecH265:  "H.265",
				avgrabber.CodecMJPEG: "MJPEG",
			}

			if name, ok := knownVideoCodecs[info.VideoCodec]; ok {
				t.Logf("video codec: %s, resolution: %dx%d, clock: %d Hz",
					name, info.Width, info.Height, info.VideoClockRate)
			} else {
				t.Errorf("unrecognised video codec: %d", info.VideoCodec)
			}
		})
	}
}

// TestSession_RTSP_Stats verifies that statistics counters are non-zero after
// receiving some frames.
func TestSession_RTSP_Stats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping RTSP integration test in short mode")
	}

	avgrabber.Init()
	t.Cleanup(avgrabber.Deinit)

	for _, cam := range testCameras {
		t.Run(cam.name, func(t *testing.T) {
			s, err := avgrabber.Open(cam.cfg)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			t.Cleanup(func() { _ = s.Close() })

			deadline := time.Now().Add(streamTimeout)
			videoFrames := 0

			for time.Now().Before(deadline) && videoFrames < minVideoFrames {
				f, ferr := s.NextFrame(50)
				if ferr != nil {
					if avgrabber.IsNotReady(ferr) {
						continue
					}

					t.Fatalf("NextFrame: %v", ferr)
				}

				if f.MediaType == avgrabber.MediaVideo && f.FrameType != avgrabber.FrameTypeParamSet {
					videoFrames++
				}
			}

			stats, err := s.GetStats()
			if err != nil {
				t.Fatalf("GetStats: %v", err)
			}

			if stats.VideoFrameTotal == 0 {
				t.Error("VideoFrameTotal is 0 after receiving frames")
			}

			if stats.VideoBytesTotal == 0 {
				t.Error("VideoBytesTotal is 0")
			}

			if stats.ElapsedMS == 0 {
				t.Error("ElapsedMS is 0")
			}

			t.Logf("stats: %+v", stats)
		})
	}
}

// ── Demuxer-level tests ───────────────────────────────────────────────────────

// TestDemuxer_RTSP_GetCodecs verifies that GetCodecs returns at least a video
// stream with a known codec type and a valid TimeScale.
func TestDemuxer_RTSP_GetCodecs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping RTSP integration test in short mode")
	}

	avgrabber.Init()
	t.Cleanup(avgrabber.Deinit)

	for _, cam := range testCameras {
		t.Run(cam.name, func(t *testing.T) {
			dmx, err := avgrabber.NewDemuxer(cam.cfg)
			if err != nil {
				t.Fatalf("NewDemuxer: %v", err)
			}

			t.Cleanup(func() { _ = dmx.Close() })

			ctx, cancel := context.WithTimeout(t.Context(), streamTimeout)
			defer cancel()

			streams, err := dmx.GetCodecs(ctx)
			if err != nil {
				t.Fatalf("GetCodecs: %v", err)
			}

			if len(streams) == 0 {
				t.Fatal("GetCodecs returned no streams")
			}

			video := streams[0]
			if video.Codec == nil {
				t.Fatal("video stream has nil codec")
			}

			switch video.Codec.Type() {
			case av.H264, av.H265, av.MJPEG:
				t.Logf("video codec: %s", video.Codec.Type())
			default:
				t.Errorf("unexpected video codec type: %s", video.Codec.Type())
			}

			if vc, ok := video.Codec.(av.VideoCodecData); ok {
				if ts := vc.TimeScale(); ts == 0 {
					t.Error("video codec TimeScale is 0")
				} else {
					t.Logf("video TimeScale: %d", ts)
				}
			}

			for _, s := range streams {
				t.Logf("stream idx=%d codec=%s", s.Idx, s.Codec.Type())
			}
		})
	}
}

// TestDemuxer_RTSP_ReadPackets reads minVideoFrames video packets and verifies
// that timestamps are monotonically non-decreasing and payloads are non-empty.
func TestDemuxer_RTSP_ReadPackets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping RTSP integration test in short mode")
	}

	avgrabber.Init()
	t.Cleanup(avgrabber.Deinit)

	for _, cam := range testCameras {
		t.Run(cam.name, func(t *testing.T) {
			dmx, err := avgrabber.NewDemuxer(cam.cfg)
			if err != nil {
				t.Fatalf("NewDemuxer: %v", err)
			}

			t.Cleanup(func() { _ = dmx.Close() })

			ctx, cancel := context.WithTimeout(t.Context(), streamTimeout)
			defer cancel()

			if _, err := dmx.GetCodecs(ctx); err != nil {
				t.Fatalf("GetCodecs: %v", err)
			}

			var (
				videoPackets int
				gotKeyFrame  bool
				prevDTS      time.Duration = -1
			)

			for videoPackets < minVideoFrames {
				pkt, err := dmx.ReadPacket(ctx)
				if err != nil {
					t.Fatalf("ReadPacket after %d video packets: %v", videoPackets, err)
				}

				if pkt.Idx != 0 {
					continue
				}

				if len(pkt.Data) == 0 {
					t.Errorf("video packet %d has empty Data", videoPackets)
				}

				if prevDTS >= 0 && pkt.DTS < prevDTS {
					t.Errorf("DTS went backwards: %v → %v (packet %d)", prevDTS, pkt.DTS, videoPackets)
				}

				prevDTS = pkt.DTS

				if pkt.KeyFrame {
					gotKeyFrame = true
				}

				videoPackets++
			}

			if !gotKeyFrame {
				t.Errorf("no keyframe received in %d video packets", videoPackets)
			}

			t.Logf("received %d video packets; last DTS=%v keyframe seen=%v",
				videoPackets, prevDTS, gotKeyFrame)
		})
	}
}

// TestDemuxer_RTSP_StopResume verifies that Pause and Resume work without
// dropping the session.
func TestDemuxer_RTSP_StopResume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping RTSP integration test in short mode")
	}

	avgrabber.Init()
	t.Cleanup(avgrabber.Deinit)

	for _, cam := range testCameras {
		t.Run(cam.name, func(t *testing.T) {
			dmx, err := avgrabber.NewDemuxer(cam.cfg)
			if err != nil {
				t.Fatalf("NewDemuxer: %v", err)
			}

			t.Cleanup(func() { _ = dmx.Close() })

			ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
			defer cancel()

			if _, err := dmx.GetCodecs(ctx); err != nil {
				t.Fatalf("GetCodecs: %v", err)
			}

			for range 10 {
				if _, err := dmx.ReadPacket(ctx); err != nil {
					t.Fatalf("ReadPacket (pre-pause): %v", err)
				}
			}

			if err := dmx.Pause(ctx); err != nil {
				t.Fatalf("Pause: %v", err)
			}

			time.Sleep(500 * time.Millisecond)

			if err := dmx.Resume(ctx); err != nil {
				t.Fatalf("Resume: %v", err)
			}

			videoAfterResume := 0

			for videoAfterResume < 10 {
				pkt, err := dmx.ReadPacket(ctx)
				if err != nil {
					t.Fatalf("ReadPacket (post-resume): %v", err)
				}

				if pkt.Idx == 0 {
					videoAfterResume++
				}
			}

			t.Logf("received %d video packets after resume", videoAfterResume)
		})
	}
}
