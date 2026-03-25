// *****************************************************
//    Copyright 2025 Videonetics Technology Pvt Ltd
// *****************************************************

/**
 * @file avgrabber_api.h
 * @brief AVGrabber public C API — RTSP client library for H.264/H.265 IP cameras.
 *
 * This is the sole public interface of the AVGrabber shared/static library.
 * It is intentionally C-compatible (C99) and designed for consumption from
 * C, C++, Go (cgo), Python (ctypes/cffi), and Java (JNA / Panama FFM).
 *
 * ## Typical pull-model usage
 *
 * @code
 * avgrabber_init();
 *
 * AVGrabberConfig cfg = {0};
 * cfg.url      = "rtsp://192.168.1.10/stream1";
 * cfg.username = "admin";
 * cfg.password = "password";
 * cfg.protocol = AVGRABBER_PROTO_TCP;
 * cfg.audio    = 1;
 *
 * AVGrabberSession* session = NULL;
 * if (avgrabber_open(&session, &cfg) != AVGRABBER_OK) { return -1; }
 *
 * for (;;) {
 *     AVGrabberFrame* frame = NULL;
 *     int rc = avgrabber_next_frame(session, 200, &frame);
 *     if (rc == AVGRABBER_OK) {
 *         const AVGrabberFrameHeader* h = avgrabber_frame_header(frame);
 *         const uint8_t*              d = avgrabber_frame_data(frame);
 *         process(h, d, h->frame_size);
 *         avgrabber_frame_release(frame);
 *     } else if (rc == AVGRABBER_ERR_AUTH_FAILED ||
 *                rc == AVGRABBER_ERR_STOPPED) {
 *         break;
 *     }
 *     // AVGRABBER_ERR_NOT_READY -> just loop again
 * }
 *
 * avgrabber_close(session);
 * avgrabber_deinit();
 * @endcode
 *
 * ## ABI stability contract
 * - AVGrabberSession and AVGrabberFrame are opaque: their internal layout is
 *   never exposed and may change between versions.
 * - AVGrabberFrameHeader, AVGrabberConfig, AVGrabberStreamInfo, and
 *   AVGrabberStats are ABI-stable. Fields will never be removed or reordered;
 *   new fields may be appended in a future minor version.
 * - AVGrabberConfig has a _reserved[32] tail. Callers MUST zero-initialise the
 *   entire struct before setting fields (AVGrabberConfig cfg = {0}), so that
 *   any future fields added to the struct default safely to zero.
 *
 * ## Thread-safety contract
 * - avgrabber_init() / avgrabber_deinit(): call once per process from a single
 *   thread; must not overlap with any other API call.
 * - Each AVGrabberSession may be used from one thread at a time (single-owner
 *   model). Concurrent avgrabber_next_frame() calls on the same session are
 *   not safe.
 * - Different sessions may be used concurrently from different threads.
 * - avgrabber_frame_header() and avgrabber_frame_data() are read-only and safe
 *   to call from any thread as long as the AVGrabberFrame has not been released.
 *
 * ## Frame ownership model (pull)
 * avgrabber_next_frame() returns a library-owned AVGrabberFrame drawn from an
 * internal pool. The frame is valid — and the pointers returned by
 * avgrabber_frame_header() / avgrabber_frame_data() remain valid — until
 * avgrabber_frame_release() is called. The frame must be released exactly once.
 * All outstanding frames must be released before avgrabber_close() is called.
 *
 * ## Push model
 * Register a callback with avgrabber_set_callback(). The callback is invoked
 * on the library's internal delivery thread. The header and data pointers are
 * valid only for the duration of the callback; copy data before returning.
 * MUST NOT call avgrabber_stop() or avgrabber_close() from within a callback.
 */

#pragma once
#ifndef AVGRABBER_API_H
#define AVGRABBER_API_H

#include <stdint.h>

/* ── Symbol visibility ──────────────────────────────────────────────────── */
#if defined(_WIN32)
#if defined(AVGRABBER_BUILDING_DLL)
#define AVGRABBER_API __declspec(dllexport)
#elif !defined(AVGRABBER_STATIC)
#define AVGRABBER_API __declspec(dllimport)
#else
#define AVGRABBER_API
#endif
#elif defined(__GNUC__) && __GNUC__ >= 4
#define AVGRABBER_API __attribute__((visibility("default")))
#else
#define AVGRABBER_API
#endif

#ifdef __cplusplus
extern "C" {
#endif

/* ══════════════════════════════════════════════════════════════════════════
 * Opaque types
 * ══════════════════════════════════════════════════════════════════════════ */

/**
 * Opaque RTSP session handle.
 * Lifecycle: avgrabber_open() → (avgrabber_stop() / avgrabber_resume())* → avgrabber_close()
 */
typedef struct AVGrabberSession AVGrabberSession;

/**
 * Opaque media frame returned by avgrabber_next_frame().
 * Valid from avgrabber_next_frame() AVGRABBER_OK until avgrabber_frame_release().
 * Access its contents only through avgrabber_frame_header() / avgrabber_frame_data().
 */
typedef struct AVGrabberFrame AVGrabberFrame;

/* ══════════════════════════════════════════════════════════════════════════
 * Constants
 * Using #define so every language FFI sees plain int literals without needing
 * to import a C enum type.
 * ══════════════════════════════════════════════════════════════════════════ */

/* Transport protocol (AVGrabberConfig.protocol) */
#define AVGRABBER_PROTO_UDP 0
#define AVGRABBER_PROTO_TCP 1

/* Frame type (AVGrabberFrameHeader.frame_type) */
/** Decoder parameter sets (SPS+PPS for H.264; VPS+SPS+PPS for H.265), Annex-B.
 *  Emitted before the first keyframe, after reconnect, and on SPS/PPS change.
 *  MJPEG/MPEG streams do not emit PARAM_SET frames. */
#define AVGRABBER_FRAME_PARAM_SET 0
/** Random-access video frame. For H.264/H.265 this is an Annex-B keyframe and a
 *  PARAM_SET frame precedes it. MJPEG currently uses KEY directly with no
 *  preceding PARAM_SET. */
#define AVGRABBER_FRAME_KEY 1
/** Non-key (delta) video frame, Annex-B. */
#define AVGRABBER_FRAME_DELTA 2
/** Raw audio payload (ADTS-framed AAC, G.711, G.722, G.726, Opus). */
#define AVGRABBER_FRAME_AUDIO 16
/** Fallback type for frames that cannot be classified. Inspect media_type and
 *  codec_type to determine how to handle the payload. MPEG currently uses this
 *  fallback path. */
#define AVGRABBER_FRAME_UNKNOWN 255

/* Media type (AVGrabberFrameHeader.media_type) */
#define AVGRABBER_MEDIA_VIDEO 0
#define AVGRABBER_MEDIA_AUDIO 1

/* Codec type (AVGrabberFrameHeader.codec_type) */
#define AVGRABBER_CODEC_MJPEG   0
#define AVGRABBER_CODEC_MPEG    1
#define AVGRABBER_CODEC_H264    2
#define AVGRABBER_CODEC_G711U   3 /**< G.711 µ-law, 8 kHz, mono */
#define AVGRABBER_CODEC_G711A   4 /**< G.711 A-law, 8 kHz, mono */
#define AVGRABBER_CODEC_L16     5 /**< Linear PCM 16-bit */
#define AVGRABBER_CODEC_AAC     6 /**< AAC, ADTS-framed */
#define AVGRABBER_CODEC_UNKNOWN 7
#define AVGRABBER_CODEC_H265    8
#define AVGRABBER_CODEC_G722    9  /**< G.722 ADPCM, 16 kHz encoded bandwidth, 8 kHz RTP clock */
#define AVGRABBER_CODEC_G726    10 /**< G.726 ADPCM, 8 kHz */
#define AVGRABBER_CODEC_OPUS    11 /**< Opus raw packets, 48 kHz RTP clock */

/* Frame flags (AVGrabberFrameHeader.flags bitmask) */
/** ntp_ms is RTCP-anchored (true camera wall-clock). If clear, it is a
 *  local-clock estimate and may drift from actual camera time. */
#define AVGRABBER_FLAG_NTP_SYNCED 0x01u
/** A clock reset or stream gap > 1 s preceded this frame (e.g. reconnect,
 *  packet-loss burst). Timestamps are not continuous across this boundary. */
#define AVGRABBER_FLAG_DISCONTINUITY 0x02u
/** This is a random-access video frame (H.264 IDR; H.265 IDR/BLA/CRA). */
#define AVGRABBER_FLAG_KEYFRAME 0x04u
/** One or more SEI (Supplemental Enhancement Information) NAL units are
 *  present in this Access Unit. */
#define AVGRABBER_FLAG_HAS_SEI 0x10u

/* Status codes — the complete set of values this API can return. */
#define AVGRABBER_OK               0    /**< Success. */
#define AVGRABBER_ERR_NULL_POINTER 3    /**< A required pointer argument was NULL. */
#define AVGRABBER_ERR_NOT_READY    10   /**< No frame available within the requested timeout. */
#define AVGRABBER_ERR_STOPPED      18   /**< The session is stopped or being destroyed. */
#define AVGRABBER_ERR_INVALID_ARG  37   /**< url, username, or password is NULL or empty. */
#define AVGRABBER_ERR_AUTH_FAILED  1101 /**< Camera rejected credentials. */

/* ══════════════════════════════════════════════════════════════════════════
 * AVGrabberFrameHeader
 *
 * ABI-stable metadata struct for one decoded media frame.
 * Designed for direct use as the source of truth for fMP4 muxing:
 *   - pts_ticks / dts_ticks provide 64-bit unwrapped media-clock timestamps
 *   - duration_ticks gives the sample duration for trun box entries
 *   - ntp_ms provides camera wall-clock for A/V sync across sessions
 *
 * Layout (56 bytes, naturally aligned — no #pragma pack required):
 *
 *  Offset  Size  Field
 *     0     4    frame_type      (int32_t)
 *     4     4    media_type      (int32_t)
 *     8     4    frame_size      (int32_t)
 *    12     1    codec_type      (uint8_t)
 *    13     1    flags           (uint8_t)
 *    14     2    _pad            (uint8_t[2])
 *    16     8    wall_clock_ms   (int64_t)
 *    24     8    ntp_ms          (int64_t)
 *    32     8    pts_ticks       (int64_t)
 *    40     8    dts_ticks       (int64_t)
 *    48     4    duration_ticks  (uint32_t)
 *    52     4    _pad2           (uint32_t)
 *  Total: 56 bytes
 *
 * Go mapping (cgo):
 *   C.AVGrabberFrameHeader — all fields map directly to their C equivalents.
 *   No unsafe pointer arithmetic required.
 *
 * Python ctypes mapping:
 *   class AVGrabberFrameHeader(ctypes.Structure):
 *       _fields_ = [("frame_type",     c_int32),
 *                   ("media_type",     c_int32),
 *                   ("frame_size",     c_int32),
 *                   ("codec_type",     c_uint8),
 *                   ("flags",          c_uint8),
 *                   ("_pad",           c_uint8 * 2),
 *                   ("wall_clock_ms",  c_int64),
 *                   ("ntp_ms",         c_int64),
 *                   ("pts_ticks",      c_int64),
 *                   ("dts_ticks",      c_int64),
 *                   ("duration_ticks", c_uint32),
 *                   ("_pad2",          c_uint32)]
 *   assert ctypes.sizeof(AVGrabberFrameHeader) == 56
 *
 * Java Panama (jextract-generated via avgrabber_api.h):
 *   AVGrabberFrameHeader.frame_type(seg), .pts_ticks(seg), etc.
 * ══════════════════════════════════════════════════════════════════════════ */
typedef struct {
  int32_t frame_type;      /**< AVGRABBER_FRAME_* constant. */
  int32_t media_type;      /**< AVGRABBER_MEDIA_VIDEO or AVGRABBER_MEDIA_AUDIO. */
  int32_t frame_size;      /**< Payload bytes; matches avgrabber_frame_data() length. */
  uint8_t codec_type;      /**< AVGRABBER_CODEC_* constant. */
  uint8_t flags;           /**< Bitmask of AVGRABBER_FLAG_* bits. */
  uint8_t _pad[2];         /**< Reserved, always zero. */
  int64_t wall_clock_ms;   /**< Wall-clock ms anchored to system_clock::now(). */
  int64_t ntp_ms;          /**< Raw NTP ms from camera (0 = not yet RTCP-synced). */
  int64_t pts_ticks;       /**< Presentation timestamp in stream clock ticks.
                            *  64-bit, never wraps. Clock rate: video_clock_rate for
                            *  video (typically 90000 Hz); audio_sample_rate for audio.
                            *  Use this as the primary timestamp for fMP4 composition. */
  int64_t dts_ticks;       /**< Decode timestamp in stream clock ticks.
                            *  Equal to pts_ticks for IP cameras that do not use
                            *  B-frames (the common case). A future flag bit will
                            *  indicate when dts_ticks independently differs. */
  uint32_t duration_ticks; /**< Sample duration in stream clock ticks.
                            *  Audio: set to AVGrabberStreamInfo.audio_samples_per_frame
                            *  once known; 0 until the first PARAM_SET or audio frame.
                            *  Video: 0 — compute from consecutive pts_ticks deltas. */
  uint32_t _pad2;          /**< Reserved, always zero. */
} AVGrabberFrameHeader;    /* assert sizeof == 56 */

/* ══════════════════════════════════════════════════════════════════════════
 * AVGrabberConfig
 *
 * Session configuration passed to avgrabber_open().
 *
 * CALLERS MUST ZERO-INITIALISE the entire struct before setting any fields:
 *
 *   AVGrabberConfig cfg = {0};
 *   cfg.url      = "rtsp://192.168.1.10/stream1";
 *   cfg.username = "admin";
 *   cfg.password = "password";
 *   cfg.protocol = AVGRABBER_PROTO_TCP;
 *
 * This guarantees that any fields added in future library versions default
 * safely to zero (disabled / use-library-default).
 * ══════════════════════════════════════════════════════════════════════════ */
typedef struct {
  const char* url;                /**< RTSP URL, null-terminated. Required. */
  const char* username;           /**< Null-terminated. Required (pass "" if none). */
  const char* password;           /**< Null-terminated. Required (pass "" if none). */
  int32_t     protocol;           /**< AVGRABBER_PROTO_TCP or AVGRABBER_PROTO_UDP. */
  int32_t     multicast;          /**< 0 = unicast (default); non-zero = multicast UDP. */
  int32_t     audio;              /**< 0 = video only (default); 1 = include audio sub-stream. */
  int32_t     connect_timeout_ms; /**< 0 → library default (5000 ms). */
  int32_t     frame_queue_depth;  /**< 0 → library default (60 frames). */
  uint8_t     _reserved[32];      /**< Must be zero. Reserved for future expansion. */
} AVGrabberConfig;

/* ══════════════════════════════════════════════════════════════════════════
 * AVGrabberFrameCallback
 *
 * Callback type for push-model frame delivery (avgrabber_set_callback).
 *
 * @param session   The session that produced the frame. Never NULL.
 * @param header    Frame metadata. Valid only for the duration of the callback.
 * @param data      Payload bytes. Exactly header->frame_size bytes. Valid only
 *                  for the duration of the callback. Copy if retention needed.
 * @param userdata  The value passed to avgrabber_set_callback(). May be NULL.
 *
 * Restrictions:
 *   - Must NOT call avgrabber_stop() or avgrabber_close() from within.
 *   - Invoked on the library's internal delivery thread; must be thread-safe.
 * ══════════════════════════════════════════════════════════════════════════ */
typedef void (*AVGrabberFrameCallback)(AVGrabberSession* session, const AVGrabberFrameHeader* header,
                                       const uint8_t* data, void* userdata);

/* ══════════════════════════════════════════════════════════════════════════
 * AVGrabberStreamInfo
 *
 * Negotiated stream parameters. Populated after the first
 * AVGRABBER_FRAME_PARAM_SET frame is received, or after the first video frame
 * for codecs that do not emit parameter-set frames (for example MJPEG/MPEG).
 * Codec fields default to AVGRABBER_CODEC_UNKNOWN until observed; numeric
 * properties default to zero.
 *
 * Layout (24 bytes, naturally aligned):
 *
 *  Offset  Size  Field
 *     0     1    video_codec             (uint8_t)
 *     1     1    audio_codec             (uint8_t)
 *     2     1    audio_channels          (uint8_t)
 *     3     1    fps                     (uint8_t)
 *     4     2    width                   (uint16_t)
 *     6     2    height                  (uint16_t)
 *     8     4    video_clock_rate        (uint32_t)
 *    12     4    audio_sample_rate       (uint32_t)
 *    16     4    audio_samples_per_frame (uint32_t)
 *    20     4    _pad                    (uint32_t)
 *  Total: 24 bytes
 *
 * Python ctypes mapping:
 *   class AVGrabberStreamInfo(ctypes.Structure):
 *       _fields_ = [("video_codec",             c_uint8),
 *                   ("audio_codec",             c_uint8),
 *                   ("audio_channels",          c_uint8),
 *                   ("fps",                     c_uint8),
 *                   ("width",                   c_uint16),
 *                   ("height",                  c_uint16),
 *                   ("video_clock_rate",        c_uint32),
 *                   ("audio_sample_rate",       c_uint32),
 *                   ("audio_samples_per_frame", c_uint32),
 *                   ("_pad",                    c_uint32)]
 *   assert ctypes.sizeof(AVGrabberStreamInfo) == 24
 * ══════════════════════════════════════════════════════════════════════════ */
typedef struct {
  uint8_t  video_codec;             /**< AVGRABBER_CODEC_* for the video track; AVGRABBER_CODEC_UNKNOWN until known. */
  uint8_t  audio_codec;             /**< AVGRABBER_CODEC_* for the audio track; AVGRABBER_CODEC_UNKNOWN if absent. */
  uint8_t  audio_channels;          /**< 1 = mono, 2 = stereo (0 if unknown). */
  uint8_t  fps;                     /**< Frames per second from SPS VUI; 0 if not signalled. */
  uint16_t width;                   /**< Video width in pixels; 0 until first SPS decoded. */
  uint16_t height;                  /**< Video height in pixels; 0 until first SPS decoded. */
  uint32_t video_clock_rate;        /**< RTP clock rate for video (typically 90000 Hz). */
  uint32_t audio_sample_rate;       /**< Audio RTP clock rate in Hz (e.g. 8000, 48000).
                                     *  This is the timebase for pts_ticks / dts_ticks on
                                     *  audio frames. Note: G.722 encodes at 16 kHz but
                                     *  has an RTP clock of 8000 Hz. */
  uint32_t audio_samples_per_frame; /**< Samples per audio frame in the audio clock domain.
                                     *  Used as duration_ticks for audio frames in fMP4 trun.
                                     *  AAC = 1024; G.711/G.726 = payload-derived (typ. 160);
                                     *  Opus = 960 (20 ms at 48 kHz). 0 until first audio frame. */
  uint32_t _pad;                    /**< Reserved — always zero. */
} AVGrabberStreamInfo;              /* 24 bytes */

/* ══════════════════════════════════════════════════════════════════════════
 * AVGrabberStats
 *
 * Runtime per-session statistics. Available immediately after
 * avgrabber_open() and updated on every frame; no PARAM_SET required.
 *
 * Design notes for foreign-language bindings (Go cgo, Python ctypes/cffi,
 * Java JNA/Panama FFM):
 *   - All fields use fixed-width integer types from <stdint.h>.
 *   - No float/double: fps is encoded as integer × 1000 to avoid float ABI
 *     differences across compilers and languages.
 *   - Field order is chosen so every field sits at its natural alignment;
 *     _pad / _pad2 are explicit filler — no compiler-inserted padding exists.
 *   - Total size is 64 bytes on all supported platforms.
 *   - Zero-initialise before calling: AVGrabberStats s = {0};
 *
 * Field offsets (for struct layout in foreign bindings):
 *   offset  0  video_bitrate_kbps  uint32
 *   offset  4  video_fps_milli     uint32
 *   offset  8  video_frames_total  uint64
 *   offset 16  video_bytes_total   uint64
 *   offset 24  audio_bitrate_kbps  uint32
 *   offset 28  _pad                uint32  (reserved, always 0)
 *   offset 32  audio_frames_total  uint64
 *   offset 40  audio_bytes_total   uint64
 *   offset 48  elapsed_ms          uint64
 *   offset 56  discontinuities     uint32
 *   offset 60  _pad2               uint32  (reserved, always 0)
 * ══════════════════════════════════════════════════════════════════════════ */
typedef struct {
  /* ── video ──────────────────────────────────────────────────────────── */
  uint32_t video_bitrate_kbps; /**< Rolling 2-second video bitrate in kbps. */
  uint32_t video_fps_milli;    /**< Rolling 2-second video frame rate × 1000
                                *  (e.g. 29970 = 29.970 fps, 30000 = 30 fps).
                                *  Divide by 1000 to get fps as a real number. */
  uint64_t video_frames_total; /**< Total video frames received since avgrabber_open(). */
  uint64_t video_bytes_total;  /**< Total video payload bytes since avgrabber_open(). */
  /* ── audio ──────────────────────────────────────────────────────────── */
  uint32_t audio_bitrate_kbps; /**< Rolling 2-second audio bitrate in kbps. */
  uint32_t _pad;               /**< Reserved — always zero. */
  uint64_t audio_frames_total; /**< Total audio frames received since avgrabber_open(). */
  uint64_t audio_bytes_total;  /**< Total audio payload bytes since avgrabber_open(). */
  /* ── session ────────────────────────────────────────────────────────── */
  uint64_t elapsed_ms;      /**< Wall-clock milliseconds since avgrabber_open(). */
  uint32_t discontinuities; /**< Count of frames carrying AVGRABBER_FLAG_DISCONTINUITY
                             *  since avgrabber_open() (clock resets / stream gaps). */
  uint32_t _pad2;           /**< Reserved — always zero. */
} AVGrabberStats;           /* 64 bytes */

/* ══════════════════════════════════════════════════════════════════════════
 * Library lifecycle
 * ══════════════════════════════════════════════════════════════════════════ */

/**
 * Initialise library-wide resources.
 *
 * Must be called once per process before any other avgrabber_* function.
 * Calling more than once per process has no additional effect.
 *
 * @return AVGRABBER_OK always.
 */
AVGRABBER_API int avgrabber_init(void);

/**
 * Release all library-wide resources.
 *
 * Must be called once per process at shutdown, after all sessions have been
 * destroyed with avgrabber_close(). Do not call any other API function after
 * this returns.
 *
 * @return AVGRABBER_OK always.
 */
AVGRABBER_API int avgrabber_deinit(void);

/**
 * Query the library version.
 *
 * @param[out] major  Incremented on incompatible ABI changes. May be NULL.
 * @param[out] minor  Incremented when new backward-compatible features are added. May be NULL.
 * @param[out] patch  Incremented for bug fixes only. May be NULL.
 */
AVGRABBER_API void avgrabber_version(int32_t* major, int32_t* minor, int32_t* patch);

/**
 * Return a static, human-readable description of a status code.
 *
 * The returned pointer is a string literal; do not free it.
 * Thread-safe. Never returns NULL.
 *
 * @param status  Any AVGRABBER_OK / AVGRABBER_ERR_* value.
 * @return        Null-terminated ASCII string.
 */
AVGRABBER_API const char* avgrabber_strerror(int status);

/* ══════════════════════════════════════════════════════════════════════════
 * Session management
 * ══════════════════════════════════════════════════════════════════════════ */

/**
 * Open an RTSP session and begin connecting to the camera in the background.
 *
 * The library starts connecting immediately. For H.264/H.265, the first
 * AVGRABBER_FRAME_PARAM_SET frame returned by avgrabber_next_frame() (or
 * delivered to the callback) signals that the stream is live. MJPEG/MPEG do
 * not emit PARAM_SET frames; their first video frame signals liveness instead.
 *
 * @param[out] session_out  Set to the new session handle on AVGRABBER_OK.
 *                          Set to NULL on error. Must not be NULL itself.
 * @param[in]  cfg          Session configuration. Must not be NULL.
 *                          Must be zero-initialised before use: AVGrabberConfig cfg = {0}.
 *
 * @return AVGRABBER_OK                *session_out is a valid handle.
 * @return AVGRABBER_ERR_NULL_POINTER  session_out or cfg is NULL.
 * @return AVGRABBER_ERR_INVALID_ARG   cfg->url is NULL or empty.
 */
AVGRABBER_API int avgrabber_open(AVGrabberSession** session_out, const AVGrabberConfig* cfg);

/**
 * Stop the stream and disconnect from the camera.
 *
 * Blocks until the internal streaming thread exits. Any avgrabber_next_frame()
 * call that is blocking will unblock and return AVGRABBER_ERR_STOPPED.
 * The session handle remains valid; call avgrabber_resume() to reconnect or
 * avgrabber_close() to destroy it.
 *
 * @return AVGRABBER_OK                Stopped.
 * @return AVGRABBER_ERR_NULL_POINTER  session is NULL.
 */
AVGRABBER_API int avgrabber_stop(AVGrabberSession* session);

/**
 * Reconnect and resume streaming on a previously stopped session.
 * No-op if the session is already running.
 *
 * @return AVGRABBER_OK                Resumed.
 * @return AVGRABBER_ERR_NULL_POINTER  session is NULL.
 */
AVGRABBER_API int avgrabber_resume(AVGrabberSession* session);

/**
 * Stop (if running) and destroy the session, freeing all associated resources.
 *
 * All outstanding AVGrabberFrame pointers obtained from avgrabber_next_frame()
 * MUST be released with avgrabber_frame_release() before calling this function.
 * The session pointer is invalid after this call and must not be used again.
 *
 * @return AVGRABBER_OK                Destroyed.
 * @return AVGRABBER_ERR_NULL_POINTER  session is NULL.
 */
AVGRABBER_API int avgrabber_close(AVGrabberSession* session);

/**
 * Register a push-model frame callback.
 *
 * At most one callback may be registered per session at a time. Calling this
 * again replaces the previous registration. Pass cb=NULL to deregister.
 *
 * When a callback is registered, avgrabber_next_frame() is still functional
 * and may be called from a separate thread; each frame is delivered to exactly
 * one consumer (first come, first served from the internal queue).
 *
 * @param session   Must not be NULL.
 * @param cb        Callback function, or NULL to deregister.
 * @param userdata  Passed through to cb unchanged. May be NULL.
 *
 * @return AVGRABBER_OK                Registered (or deregistered).
 * @return AVGRABBER_ERR_NULL_POINTER  session is NULL.
 */
AVGRABBER_API int avgrabber_set_callback(AVGrabberSession* session, AVGrabberFrameCallback cb, void* userdata);

/**
 * Query negotiated stream parameters.
 *
 * Returns AVGRABBER_ERR_NOT_READY until the first AVGRABBER_FRAME_PARAM_SET
 * frame has been received, or until the first video frame for codecs that do
 * not emit parameter-set frames.
 *
 * @param session    Must not be NULL.
 * @param[out] info  Populated on AVGRABBER_OK. Must not be NULL.
 *
 * @return AVGRABBER_OK            info is populated.
 * @return AVGRABBER_ERR_NOT_READY First PARAM_SET frame not yet received.
 * @return AVGRABBER_ERR_NULL_POINTER session or info is NULL.
 */
AVGRABBER_API int avgrabber_stream_info(AVGrabberSession* session, AVGrabberStreamInfo* info);

/**
 * Query runtime statistics for a session.
 *
 * Unlike avgrabber_stream_info(), this returns AVGRABBER_OK immediately after
 * avgrabber_open() — no PARAM_SET frame is required. All counters start at zero
 * and grow as frames are received.
 *
 * Thread-safe; may be called from any thread at any time while the session is open.
 *
 * @param session      Must not be NULL.
 * @param[out] stats   Caller-allocated struct; zero-initialise before calling:
 *                     AVGrabberStats stats = {0};
 *                     Must not be NULL.
 *
 * @return AVGRABBER_OK            stats is populated.
 * @return AVGRABBER_ERR_NULL_POINTER session or stats is NULL.
 */
AVGRABBER_API int avgrabber_stats(AVGrabberSession* session, AVGrabberStats* stats);

/* ══════════════════════════════════════════════════════════════════════════
 * Pull-model frame retrieval
 * ══════════════════════════════════════════════════════════════════════════ */

/**
 * Retrieve the next available media frame (library-owned buffer).
 *
 * Blocks according to timeout_ms:
 *   timeout_ms > 0   Block up to N milliseconds; return AVGRABBER_ERR_NOT_READY if no frame arrives.
 *   timeout_ms = 0   Non-blocking; return AVGRABBER_ERR_NOT_READY immediately if queue is empty.
 *   timeout_ms < 0   Block indefinitely until a frame arrives or the session is stopped.
 *
 * On AVGRABBER_OK:
 *   *frame_out holds a valid AVGrabberFrame. The caller MUST call
 *   avgrabber_frame_release(*frame_out) when done. Until then, the pointers
 *   returned by avgrabber_frame_header() and avgrabber_frame_data() remain valid.
 *
 * @param session       Must not be NULL.
 * @param timeout_ms    Timeout in milliseconds (see above).
 * @param[out] frame_out Set to the frame pointer on AVGRABBER_OK. Must not be NULL.
 *
 * @return AVGRABBER_OK               Frame available; *frame_out is valid.
 * @return AVGRABBER_ERR_NOT_READY    No frame within the timeout.
 * @return AVGRABBER_ERR_STOPPED      Session is stopped or being destroyed.
 * @return AVGRABBER_ERR_AUTH_FAILED  Camera rejected credentials.
 * @return AVGRABBER_ERR_NULL_POINTER session or frame_out is NULL.
 */
AVGRABBER_API int avgrabber_next_frame(AVGrabberSession* session, int32_t timeout_ms, AVGrabberFrame** frame_out);

/**
 * Return the metadata for a frame obtained from avgrabber_next_frame().
 *
 * The returned pointer is valid until avgrabber_frame_release(frame).
 * Never returns NULL for a valid, unreleased frame.
 */
AVGRABBER_API const AVGrabberFrameHeader* avgrabber_frame_header(const AVGrabberFrame* frame);

/**
 * Return the payload bytes for a frame obtained from avgrabber_next_frame().
 *
 * Exactly avgrabber_frame_header(frame)->frame_size bytes are accessible.
 * The returned pointer is valid until avgrabber_frame_release(frame).
 * Never returns NULL for a valid, unreleased frame.
 *
 * ### Video payload format
 * Annex-B: each NAL unit is prefixed with the four-byte start code
 * 0x00 0x00 0x00 0x01. A PARAM_SET frame contains SPS+PPS (H.264) or
 * VPS+SPS+PPS (H.265). KEY frames contain only the keyframe slices.
 * PARAM_SET payloads go directly into the avcC / hvcC box for fMP4.
 *
 * ### Audio payload format
 * Raw codec data: AAC = ADTS-framed (7-byte header prepended by library);
 * G.711 = raw 8-bit PCM at 8 kHz; G.722 = 4-bit ADPCM at 16 kHz encoded
 * bandwidth; G.726 = ADPCM at 8 kHz; Opus = raw Opus packets.
 */
AVGRABBER_API const uint8_t* avgrabber_frame_data(const AVGrabberFrame* frame);

/**
 * Release a frame back to the library's internal buffer pool.
 *
 * Must be called exactly once per frame returned by avgrabber_next_frame().
 * After this call, all pointers obtained from avgrabber_frame_header() and
 * avgrabber_frame_data() for this frame become invalid.
 *
 * Passing NULL is a no-op.
 */
AVGRABBER_API void avgrabber_frame_release(AVGrabberFrame* frame);

#ifdef __cplusplus
}
#endif
#endif /* AVGRABBER_API_H */
