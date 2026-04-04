package metrics

const (
	MetricLiveViewStartupMs   = "live_view_startup_ms"
	MetricRTSPSessionSetupMs  = "rtsp_session_setup_ms"
	MetricRecordingGapSeconds = "recording_gap_seconds"
	MetricAPIResponseMs       = "api_response_ms"
	MetricConsumerAddMs       = "consumer_add_ms"
	MetricFragmentGapMs       = "fragment_gap_ms"

	MetricPlaybackStartupMs = "playback_startup_ms"
	MetricTimelineQueryMs   = "timeline_query_ms"
	MetricSegmentOpenMs     = "segment_open_ms"
	MetricRecToLiveTransMs  = "rec_to_live_transition_ms"
	MetricSeekLatencyMs     = "seek_latency_ms"

	MetricWriteThroughputBps    = "write_throughput_bps"
	MetricConsumerRotationCount = "consumer_rotation_count"
	MetricConsumerSkipCount     = "consumer_skip_count"
)
