import { useEffect, useRef } from 'react';
import type { WsFrameAnalytics } from '../types';
import styles from './detection-overlay.module.css';

interface DetectionOverlayProps {
  analytics: WsFrameAnalytics | null;
  videoWidth: number;
  videoHeight: number;
}

const CLASS_LABELS: Record<number, string> = {
  0: 'person',
  1: 'vehicle',
  2: 'bicycle',
  3: 'animal',
};

export function DetectionOverlay({ analytics, videoWidth, videoHeight }: DetectionOverlayProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    canvas.width = videoWidth || 1920;
    canvas.height = videoHeight || 1080;

    ctx.clearRect(0, 0, canvas.width, canvas.height);

    if (!analytics?.objects) return;

    const scaleX = canvas.width / analytics.refWidth;
    const scaleY = canvas.height / analytics.refHeight;

    for (const det of analytics.objects) {
      const x = det.x * scaleX;
      const y = det.y * scaleY;
      const w = det.w * scaleX;
      const h = det.h * scaleY;

      ctx.strokeStyle = det.isEvent ? '#ff4444' : '#44ff44';
      ctx.lineWidth = 2;
      ctx.strokeRect(x, y, w, h);

      const label = CLASS_LABELS[det.classId] ?? `cls${det.classId}`;
      const text = `${label} ${det.confidence}%`;
      ctx.font = '13px "JetBrains Mono", monospace';
      const metrics = ctx.measureText(text);
      const bgH = 18;

      ctx.fillStyle = det.isEvent ? 'rgba(255, 68, 68, 0.75)' : 'rgba(68, 255, 68, 0.75)';
      ctx.fillRect(x, y - bgH, metrics.width + 8, bgH);

      ctx.fillStyle = '#000';
      ctx.fillText(text, x + 4, y - 4);
    }
  }, [analytics, videoWidth, videoHeight]);

  return <canvas ref={canvasRef} className={styles.canvas} />;
}
