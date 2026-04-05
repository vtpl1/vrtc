import type { PlaybackMode } from '../types';
import styles from './playback-controls.module.css';

interface PlaybackControlsProps {
  mode: PlaybackMode;
  connected: boolean;
  paused: boolean;
  wallClock: Date | null;
  onPause(): void;
  onResume(): void;
  onSkip(offset: string): void;
  onGoLive(): void;
}

function formatWallClock(date: Date | null): string {
  if (!date) return '--:--:--';
  return date.toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    fractionalSecondDigits: 1,
  } as Intl.DateTimeFormatOptions);
}

export function PlaybackControls({
  mode,
  connected,
  paused,
  wallClock,
  onPause,
  onResume,
  onSkip,
  onGoLive,
}: PlaybackControlsProps) {
  return (
    <div className={styles.bar}>
      <div className={styles.group}>
        <button
          className="btn btn--sm"
          onClick={() => onSkip('-30s')}
          disabled={!connected}
          aria-label="Skip back 30 seconds"
        >
          -30s
        </button>
        <button
          className="btn btn--sm"
          onClick={() => onSkip('-10s')}
          disabled={!connected}
          aria-label="Skip back 10 seconds"
        >
          -10s
        </button>

        {paused ? (
          <button
            className="btn btn--primary btn--sm"
            onClick={onResume}
            disabled={!connected}
            aria-label="Play"
          >
            Play
          </button>
        ) : (
          <button
            className="btn btn--sm"
            onClick={onPause}
            disabled={!connected}
            aria-label="Pause"
          >
            Pause
          </button>
        )}

        <button
          className="btn btn--sm"
          onClick={() => onSkip('10s')}
          disabled={!connected}
          aria-label="Skip forward 10 seconds"
        >
          +10s
        </button>
        <button
          className="btn btn--sm"
          onClick={() => onSkip('30s')}
          disabled={!connected}
          aria-label="Skip forward 30 seconds"
        >
          +30s
        </button>
      </div>

      <div className={styles.center}>
        <span className={styles.clock}>{formatWallClock(wallClock)}</span>
        <span className={`badge ${mode === 'live' ? 'badge--danger' : 'badge--accent'}`}>
          {mode.toUpperCase()}
        </span>
        {!connected && <span className="badge badge--warning">DISCONNECTED</span>}
      </div>

      <div className={styles.group}>
        <button
          className="btn btn--danger btn--sm"
          onClick={onGoLive}
          disabled={!connected || mode === 'live'}
        >
          Go Live
        </button>
      </div>
    </div>
  );
}
