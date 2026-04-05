import { useCallback, useMemo, useRef, useState } from 'react';
import { skipToken } from '@reduxjs/toolkit/query/react';
import { useGetCameraTimelineQuery } from '../api';
import type { CameraInfo } from '../api';
import { useMsePlayback } from '../hooks/use-mse-playback';
import { CameraSelector } from '../components/camera-selector';
import { VideoPlayer } from '../components/video-player';
import { PlaybackControls } from '../components/playback-controls';
import { TimelineBar } from '../components/timeline-bar';
import { SeekDebugPanel } from '../components/seek-debug-panel';
import styles from './player.module.css';

// Default timeline range: last 24 hours
function defaultRange() {
  const end = new Date();
  const start = new Date(end.getTime() - 24 * 60 * 60 * 1000);
  return { start, end };
}

export default function PlayerRoute() {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [camera, setCamera] = useState<CameraInfo | null>(null);
  const [startTime, setStartTime] = useState<Date | null>(null);
  const [paused, setPaused] = useState(false);
  const [videoCurrentTime, setVideoCurrentTime] = useState(0);
  const [enableAnalytics, setEnableAnalytics] = useState(true);

  const range = useMemo(defaultRange, []);

  const {
    state: playback,
    seek,
    skip,
    pause,
    resume,
    goLive,
    mediaTimeToWallClock,
  } = useMsePlayback(videoRef, {
    cameraId: camera?.cameraId ?? null,
    startTime,
    analytics: enableAnalytics,
  });

  // Timeline data
  const timelineQuery = useGetCameraTimelineQuery(
    camera
      ? {
          cameraId: camera.cameraId,
          start: range.start.toISOString(),
          end: range.end.toISOString(),
        }
      : skipToken,
  );

  const timelineSegments = timelineQuery.data?.items ?? [];

  const handleSeek = useCallback(
    (target: Date) => {
      seek(target);
    },
    [seek],
  );

  const handlePause = useCallback(() => {
    pause();
    setPaused(true);
  }, [pause]);

  const handleResume = useCallback(() => {
    resume();
    setPaused(false);
  }, [resume]);

  const handleTimeUpdate = useCallback((t: number) => {
    setVideoCurrentTime(t);
  }, []);

  // Compute displayed wall-clock from video.currentTime
  const displayedWallClock = mediaTimeToWallClock(videoCurrentTime) ?? playback.wallClock;

  return (
    <div className={styles.page}>
      {/* Header bar */}
      <div className={styles.toolbar}>
        <CameraSelector
          selectedId={camera?.cameraId ?? null}
          onSelect={(cam) => {
            setCamera(cam);
            setStartTime(null); // start live
            setPaused(false);
          }}
        />

        <label className={styles.checkLabel}>
          <input
            type="checkbox"
            checked={enableAnalytics}
            onChange={(e) => setEnableAnalytics(e.target.checked)}
          />
          <span className="text-sm">Analytics overlay</span>
        </label>

        {camera && (
          <div className="row">
            <span className="badge">{camera.codec}</span>
            <span className="badge">{camera.resolution}</span>
            <span className="badge">{camera.fps} fps</span>
            {camera.recording && <span className="badge badge--danger">REC</span>}
          </div>
        )}
      </div>

      {/* Video area */}
      {camera ? (
        <div className={styles.playerArea}>
          <VideoPlayer
            videoRef={videoRef}
            analytics={playback.lastAnalytics}
            onTimeUpdate={handleTimeUpdate}
          />

          <PlaybackControls
            mode={playback.mode}
            connected={playback.connected}
            paused={paused}
            wallClock={displayedWallClock}
            onPause={handlePause}
            onResume={handleResume}
            onSkip={skip}
            onGoLive={goLive}
          />

          <TimelineBar
            segments={timelineSegments}
            rangeStart={range.start}
            rangeEnd={range.end}
            currentWallClock={displayedWallClock}
            onSeek={handleSeek}
          />
        </div>
      ) : (
        <div className="empty-state">
          <h2 className="empty-state__title">No camera selected</h2>
          <p>Select a camera above to start live or recorded playback.</p>
        </div>
      )}

      {/* Debug panel */}
      {camera && (
        <SeekDebugPanel
          playback={playback}
          videoCurrentTime={videoCurrentTime}
          mediaTimeToWallClock={mediaTimeToWallClock}
          onSeek={handleSeek}
          onSkip={skip}
        />
      )}
    </div>
  );
}

// React Router lazy module convention
export const Component = PlayerRoute;
