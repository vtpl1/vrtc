import { useState } from 'react';
import type { PlaybackState } from '../types';
import styles from './seek-debug-panel.module.css';

interface SeekDebugPanelProps {
  playback: PlaybackState;
  videoCurrentTime: number;
  mediaTimeToWallClock(t: number): Date | null;
  onSeek(target: Date): void;
  onSkip(offset: string): void;
}

export function SeekDebugPanel({
  playback,
  videoCurrentTime,
  mediaTimeToWallClock,
  onSeek,
  onSkip,
}: SeekDebugPanelProps) {
  const [seekInput, setSeekInput] = useState('');
  const [skipInput, setSkipInput] = useState('-30s');

  const computedWallClock = mediaTimeToWallClock(videoCurrentTime);

  return (
    <div className={styles.panel}>
      <div className={styles.grid}>
        <div className={styles.field}>
          <span className={styles.label}>Mode</span>
          <span className={`badge ${playback.mode === 'live' ? 'badge--danger' : 'badge--accent'}`}>
            {playback.mode}
          </span>
        </div>
        <div className={styles.field}>
          <span className={styles.label}>Connected</span>
          <span className={`badge ${playback.connected ? 'badge--success' : 'badge--warning'}`}>
            {playback.connected ? 'yes' : 'no'}
          </span>
        </div>
        <div className={styles.field}>
          <span className={styles.label}>MIME</span>
          <code className={styles.value}>{playback.mimeType || '—'}</code>
        </div>
        <div className={styles.field}>
          <span className={styles.label}>Seek seq</span>
          <code className={styles.value}>{playback.seekSeq}</code>
        </div>
        <div className={styles.field}>
          <span className={styles.label}>Server wallClock</span>
          <code className={styles.value}>{playback.wallClock?.toISOString() ?? '—'}</code>
        </div>
        <div className={styles.field}>
          <span className={styles.label}>Stream anchor</span>
          <code className={styles.value}>
            {playback.streamStartWallClock?.toISOString() ?? '—'}
          </code>
        </div>
        <div className={styles.field}>
          <span className={styles.label}>video.currentTime</span>
          <code className={styles.value}>{videoCurrentTime.toFixed(3)}s</code>
        </div>
        <div className={styles.field}>
          <span className={styles.label}>Computed wallClock</span>
          <code className={styles.value}>{computedWallClock?.toISOString() ?? '—'}</code>
        </div>
        {playback.lastAnalytics && (
          <>
            <div className={styles.field}>
              <span className={styles.label}>Analytics capture</span>
              <code className={styles.value}>{playback.lastAnalytics.capture}</code>
            </div>
            <div className={styles.field}>
              <span className={styles.label}>Objects</span>
              <code className={styles.value}>
                {playback.lastAnalytics.objects?.length ?? 0} (P:
                {playback.lastAnalytics.peopleCount} V:{playback.lastAnalytics.vehicleCount})
              </code>
            </div>
          </>
        )}
        {playback.error && (
          <div className={`${styles.field} ${styles.fieldFull}`}>
            <span className={styles.label}>Error</span>
            <code className={styles.error}>{playback.error}</code>
          </div>
        )}
      </div>

      <div className={styles.actions}>
        <div className={styles.inputGroup}>
          <label htmlFor="seek-input" className={styles.label}>
            Seek to (RFC3339)
          </label>
          <input
            id="seek-input"
            className={styles.input}
            type="text"
            placeholder="2026-04-06T12:00:00Z"
            value={seekInput}
            onChange={(e) => setSeekInput(e.target.value)}
          />
          <button
            className="btn btn--primary btn--sm"
            onClick={() => {
              const d = new Date(seekInput);
              if (!isNaN(d.getTime())) onSeek(d);
            }}
            disabled={!playback.connected}
          >
            Seek
          </button>
        </div>
        <div className={styles.inputGroup}>
          <label htmlFor="skip-input" className={styles.label}>
            Skip (Go duration)
          </label>
          <input
            id="skip-input"
            className={styles.input}
            type="text"
            placeholder="-30s"
            value={skipInput}
            onChange={(e) => setSkipInput(e.target.value)}
          />
          <button
            className="btn btn--sm"
            onClick={() => onSkip(skipInput)}
            disabled={!playback.connected}
          >
            Skip
          </button>
        </div>
      </div>
    </div>
  );
}
