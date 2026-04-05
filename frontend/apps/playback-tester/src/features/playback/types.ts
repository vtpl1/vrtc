/** WebSocket text frame messages from the server */
export interface MseCodecMessage {
  type: 'mse';
  value: string;
}

export interface PlaybackInfoMessage {
  type: 'playback_info';
  mode: 'recorded' | 'first_available';
  actualStartWallClock: string;
  wallClock: string;
}

export interface ModeChangeMessage {
  type: 'mode_change';
  mode: 'live';
  wallClock: string;
}

export interface SeekedMessage {
  type: 'seeked';
  wallClock: string;
  mode: 'recorded' | 'live';
  codecChanged: boolean;
  codecs?: string;
  gap?: boolean;
  seq?: number;
}

export interface TimingMessage {
  type: 'timing';
  wallClock: string;
}

export interface ErrorMessage {
  type: 'error';
  error: string;
}

export type ServerTextMessage =
  | MseCodecMessage
  | PlaybackInfoMessage
  | ModeChangeMessage
  | SeekedMessage
  | TimingMessage
  | ErrorMessage;

/** Real-time analytics delivered via WS text frames */
export interface WsDetection {
  x: number;
  y: number;
  w: number;
  h: number;
  classId: number;
  confidence: number;
  trackId: number;
  isEvent: boolean;
}

export interface WsFrameAnalytics {
  siteId: number;
  channelId: number;
  framePts: number;
  capture: string;
  captureEnd: string;
  inference: string;
  refWidth: number;
  refHeight: number;
  vehicleCount: number;
  peopleCount: number;
  objects: WsDetection[] | null;
}

export type PlaybackMode = 'live' | 'recorded' | 'first_available';

export interface PlaybackState {
  mode: PlaybackMode;
  connected: boolean;
  wallClock: Date | null;
  streamStartWallClock: Date | null;
  mimeType: string;
  error: string | null;
  seekSeq: number;
  lastAnalytics: WsFrameAnalytics | null;
}
