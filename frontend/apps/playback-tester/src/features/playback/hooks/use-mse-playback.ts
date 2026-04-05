import { useCallback, useEffect, useRef, useState } from 'react';
import type { PlaybackMode, PlaybackState, ServerTextMessage, WsFrameAnalytics } from '../types';

interface UseMsePlaybackOptions {
  cameraId: string | null;
  startTime: Date | null;
  analytics?: boolean;
}

const INITIAL_STATE: PlaybackState = {
  mode: 'live',
  connected: false,
  wallClock: null,
  streamStartWallClock: null,
  mimeType: '',
  error: null,
  seekSeq: 0,
  lastAnalytics: null,
};

export function useMsePlayback(
  videoRef: React.RefObject<HTMLVideoElement | null>,
  options: UseMsePlaybackOptions,
) {
  const { cameraId, startTime, analytics } = options;
  const [state, setState] = useState<PlaybackState>(INITIAL_STATE);

  const wsRef = useRef<WebSocket | null>(null);
  const mediaSourceRef = useRef<MediaSource | null>(null);
  const sourceBufferRef = useRef<SourceBuffer | null>(null);
  const bufferQueueRef = useRef<ArrayBuffer[]>([]);
  const seekSeqRef = useRef(0);
  const streamStartRef = useRef<Date | null>(null);

  // Flush queued buffers into SourceBuffer
  const flushQueue = useCallback(() => {
    const sb = sourceBufferRef.current;
    if (!sb || sb.updating || bufferQueueRef.current.length === 0) return;
    const chunk = bufferQueueRef.current.shift();
    if (chunk) {
      try {
        sb.appendBuffer(chunk);
      } catch {
        // QuotaExceededError — remove old data and retry
        if (sb.buffered.length > 0) {
          const removeEnd = sb.buffered.start(0) + 10;
          if (removeEnd < sb.buffered.end(sb.buffered.length - 1)) {
            sb.remove(sb.buffered.start(0), removeEnd);
          }
        }
      }
    }
  }, []);

  // Create or recreate the SourceBuffer for a given MIME type
  const ensureSourceBuffer = useCallback(
    (ms: MediaSource, mime: string) => {
      if (sourceBufferRef.current) {
        // If codec changed, use changeType if available
        if (typeof sourceBufferRef.current.changeType === 'function') {
          sourceBufferRef.current.changeType(mime);
          return;
        }
        // Fallback: remove old and create new
        try {
          ms.removeSourceBuffer(sourceBufferRef.current);
        } catch {
          // ignore
        }
      }
      const sb = ms.addSourceBuffer(mime);
      sb.mode = 'segments';
      sb.addEventListener('updateend', flushQueue);
      sourceBufferRef.current = sb;
      bufferQueueRef.current = [];
    },
    [flushQueue],
  );

  // Handle text messages from the server
  const handleTextMessage = useCallback(
    (raw: string) => {
      let msg: ServerTextMessage | WsFrameAnalytics;
      try {
        msg = JSON.parse(raw);
      } catch {
        return;
      }

      // Analytics frame (no "type" field, has "capture")
      if (!('type' in msg) && 'capture' in msg) {
        setState((s) => ({ ...s, lastAnalytics: msg as WsFrameAnalytics }));
        return;
      }

      const textMsg = msg as ServerTextMessage;

      switch (textMsg.type) {
        case 'mse': {
          // Codec negotiation
          const ms = mediaSourceRef.current;
          if (ms && ms.readyState === 'open') {
            ensureSourceBuffer(ms, textMsg.value);
          }
          setState((s) => ({ ...s, mimeType: textMsg.value }));
          break;
        }
        case 'playback_info': {
          const wc = new Date(textMsg.actualStartWallClock);
          streamStartRef.current = wc;
          setState((s) => ({
            ...s,
            mode: textMsg.mode,
            wallClock: new Date(textMsg.wallClock),
            streamStartWallClock: wc,
          }));
          break;
        }
        case 'mode_change': {
          setState((s) => ({
            ...s,
            mode: textMsg.mode as PlaybackMode,
            wallClock: new Date(textMsg.wallClock),
          }));
          break;
        }
        case 'seeked': {
          const wc = new Date(textMsg.wallClock);
          streamStartRef.current = wc;
          if (textMsg.codecChanged && textMsg.codecs) {
            const ms = mediaSourceRef.current;
            if (ms && ms.readyState === 'open') {
              ensureSourceBuffer(ms, textMsg.codecs);
            }
          }
          setState((s) => ({
            ...s,
            mode: textMsg.mode,
            wallClock: wc,
            streamStartWallClock: wc,
          }));
          break;
        }
        case 'timing': {
          setState((s) => ({ ...s, wallClock: new Date(textMsg.wallClock) }));
          break;
        }
        case 'error': {
          setState((s) => ({ ...s, error: textMsg.error }));
          break;
        }
      }
    },
    [ensureSourceBuffer],
  );

  // Handle binary messages (fMP4 fragments)
  const handleBinaryMessage = useCallback(
    (data: ArrayBuffer) => {
      bufferQueueRef.current.push(data);
      flushQueue();
    },
    [flushQueue],
  );

  // Connect/disconnect
  useEffect(() => {
    if (!cameraId) return;

    const video = videoRef.current;
    if (!video) return;

    // Reset state
    setState({ ...INITIAL_STATE });
    bufferQueueRef.current = [];
    sourceBufferRef.current = null;

    // Create MediaSource
    const ms = new MediaSource();
    mediaSourceRef.current = ms;
    video.src = URL.createObjectURL(ms);

    // Build WS URL
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const params = new URLSearchParams({ cameraId });
    if (startTime) params.set('start', startTime.toISOString());
    if (analytics) params.set('analytics', 'true');
    const wsUrl = `${proto}//${location.host}/api/cameras/ws/stream?${params}`;

    let ws: WebSocket;

    ms.addEventListener('sourceopen', () => {
      ws = new WebSocket(wsUrl);
      ws.binaryType = 'arraybuffer';
      wsRef.current = ws;

      ws.addEventListener('open', () => {
        ws.send(JSON.stringify({ type: 'mse' }));
        setState((s) => ({ ...s, connected: true }));
      });

      ws.addEventListener('message', (ev) => {
        if (typeof ev.data === 'string') {
          handleTextMessage(ev.data);
        } else {
          handleBinaryMessage(ev.data as ArrayBuffer);
        }
      });

      ws.addEventListener('close', () => {
        setState((s) => ({ ...s, connected: false }));
      });

      ws.addEventListener('error', () => {
        setState((s) => ({
          ...s,
          connected: false,
          error: 'WebSocket connection failed',
        }));
      });
    });

    // Auto-play once data arrives
    video.addEventListener(
      'canplay',
      () => {
        video.play().catch(() => {
          // Autoplay blocked — user interaction needed
        });
      },
      { once: true },
    );

    return () => {
      wsRef.current?.close();
      wsRef.current = null;
      if (ms.readyState === 'open') {
        try {
          ms.endOfStream();
        } catch {
          // ignore
        }
      }
      URL.revokeObjectURL(video.src);
      video.src = '';
      mediaSourceRef.current = null;
      sourceBufferRef.current = null;
    };
  }, [cameraId, startTime, analytics, videoRef, handleTextMessage, handleBinaryMessage]);

  // --- Commands ---

  const seek = useCallback((targetWallClock: Date) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const seq = ++seekSeqRef.current;
    ws.send(
      JSON.stringify({
        type: 'mse',
        value: 'seek',
        time: targetWallClock.toISOString(),
        seq,
      }),
    );
    setState((s) => ({ ...s, seekSeq: seq }));
  }, []);

  const skip = useCallback((offsetStr: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const seq = ++seekSeqRef.current;
    ws.send(
      JSON.stringify({
        type: 'mse',
        value: 'skip',
        offset: offsetStr,
        seq,
      }),
    );
  }, []);

  const pause = useCallback(() => {
    wsRef.current?.send(JSON.stringify({ type: 'mse', value: 'pause' }));
    videoRef.current?.pause();
  }, [videoRef]);

  const resume = useCallback(() => {
    wsRef.current?.send(JSON.stringify({ type: 'mse', value: 'resume' }));
    videoRef.current?.play();
  }, [videoRef]);

  const goLive = useCallback(() => {
    seek(new Date('9999-12-31T23:59:59Z')); // "now" — server switches to live
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      // Use the dedicated "seek to now" shorthand
      const seq = ++seekSeqRef.current;
      ws.send(JSON.stringify({ type: 'mse', value: 'seek', time: 'now', seq }));
    }
  }, [seek]);

  // Wall-clock ↔ media time helpers
  const mediaTimeToWallClock = useCallback((mediaTimeSec: number): Date | null => {
    const anchor = streamStartRef.current;
    if (!anchor) return null;
    return new Date(anchor.getTime() + mediaTimeSec * 1000);
  }, []);

  const wallClockToMediaTime = useCallback((wallClock: Date): number | null => {
    const anchor = streamStartRef.current;
    if (!anchor) return null;
    return (wallClock.getTime() - anchor.getTime()) / 1000;
  }, []);

  return {
    state,
    seek,
    skip,
    pause,
    resume,
    goLive,
    mediaTimeToWallClock,
    wallClockToMediaTime,
  } as const;
}
