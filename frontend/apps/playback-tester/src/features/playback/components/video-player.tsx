import { useEffect, useRef, useState } from 'react';
import { DetectionOverlay } from './detection-overlay';
import type { WsFrameAnalytics } from '../types';
import styles from './video-player.module.css';

interface VideoPlayerProps {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  analytics: WsFrameAnalytics | null;
  onTimeUpdate?(currentTime: number): void;
}

export function VideoPlayer({ videoRef, analytics, onTimeUpdate }: VideoPlayerProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [dimensions, setDimensions] = useState({ width: 1920, height: 1080 });

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    function handleResize() {
      if (video && video.videoWidth > 0) {
        setDimensions({
          width: video.videoWidth,
          height: video.videoHeight,
        });
      }
    }

    function handleTimeUpdate() {
      onTimeUpdate?.(video!.currentTime);
    }

    video.addEventListener('resize', handleResize);
    video.addEventListener('loadedmetadata', handleResize);
    video.addEventListener('timeupdate', handleTimeUpdate);

    return () => {
      video.removeEventListener('resize', handleResize);
      video.removeEventListener('loadedmetadata', handleResize);
      video.removeEventListener('timeupdate', handleTimeUpdate);
    };
  }, [videoRef, onTimeUpdate]);

  return (
    <div ref={containerRef} className={styles.container}>
      <video ref={videoRef} className={styles.video} playsInline muted autoPlay />
      <DetectionOverlay
        analytics={analytics}
        videoWidth={dimensions.width}
        videoHeight={dimensions.height}
      />
    </div>
  );
}
