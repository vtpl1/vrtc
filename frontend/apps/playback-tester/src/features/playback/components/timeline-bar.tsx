import { useCallback, useRef } from 'react';
import type { TimelineSummary } from '../api';
import styles from './timeline-bar.module.css';

interface TimelineBarProps {
  segments: TimelineSummary[];
  rangeStart: Date;
  rangeEnd: Date;
  currentWallClock: Date | null;
  onSeek(target: Date): void;
}

function toFraction(date: Date, start: Date, end: Date): number {
  const range = end.getTime() - start.getTime();
  if (range <= 0) return 0;
  return Math.max(0, Math.min(1, (date.getTime() - start.getTime()) / range));
}

function formatTime(date: Date): string {
  return date.toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

export function TimelineBar({
  segments,
  rangeStart,
  rangeEnd,
  currentWallClock,
  onSeek,
}: TimelineBarProps) {
  const trackRef = useRef<HTMLDivElement>(null);

  const handleClick = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      const track = trackRef.current;
      if (!track) return;
      const rect = track.getBoundingClientRect();
      const fraction = (e.clientX - rect.left) / rect.width;
      const targetMs =
        rangeStart.getTime() + fraction * (rangeEnd.getTime() - rangeStart.getTime());
      onSeek(new Date(targetMs));
    },
    [rangeStart, rangeEnd, onSeek],
  );

  const cursorFraction = currentWallClock
    ? toFraction(currentWallClock, rangeStart, rangeEnd)
    : null;

  return (
    <div className={styles.wrapper}>
      <span className={styles.label}>{formatTime(rangeStart)}</span>
      <div
        ref={trackRef}
        className={styles.track}
        onClick={handleClick}
        role="slider"
        aria-label="Timeline scrubber"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={cursorFraction != null ? Math.round(cursorFraction * 100) : 0}
        tabIndex={0}
      >
        {segments.map((seg, i) => {
          const left = toFraction(new Date(seg.start), rangeStart, rangeEnd);
          const right = toFraction(new Date(seg.end), rangeStart, rangeEnd);
          return (
            <div
              key={i}
              className={`${styles.segment} ${seg.hasEvents ? styles.segmentEvent : ''}`}
              style={{
                left: `${left * 100}%`,
                width: `${(right - left) * 100}%`,
              }}
            />
          );
        })}
        {cursorFraction != null && (
          <div className={styles.cursor} style={{ left: `${cursorFraction * 100}%` }} />
        )}
      </div>
      <span className={styles.label}>{formatTime(rangeEnd)}</span>
    </div>
  );
}
