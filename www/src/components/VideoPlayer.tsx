import { Box, Button, Text } from "@chakra-ui/react";
import { useRef, useState, useCallback, useEffect } from "react";
import { useDebounce } from "@uidotdev/usehooks";
import useWebSocket, { ReadyState } from "react-use-websocket";

const VideoPlayer = () => {
  const svgRef = useRef<SVGSVGElement | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const [videoWidth, setVideoWidth] = useState<number>(0);
  const [videoHeight, setVideoHeight] = useState<number>(0);
  const [siteId, setSiteId] = useState<number>(-1);
  const [channelId, setChannelId] = useState<number>(-1);
  const [appId, setAppId] = useState<number>(0);
  const [liveOrRecording, setLiveOrRecording] = useState<boolean>(true);
  const [streamId, setStreamId] = useState<number>(0);
  const [timeStamp, setTimeStamp] = useState<number>(-1);

  const debouncedSiteId = useDebounce(siteId, 500);
  const debouncedChannelId = useDebounce(channelId, 500);
  const debouncedAppId = useDebounce(appId, 500);
  const debouncedLiveOrRecording = useDebounce(liveOrRecording, 500);
  const debouncedStreamId = useDebounce(streamId, 500);
  const debouncedTimeStamp = useDebounce(timeStamp, 500);

  const videoRef = useRef<HTMLVideoElement | null>(null);
  const msRef = useRef<MediaSource | null>(null);

  const [url, setUrl] = useState<string | null>(
    "ws://localhost:1984/api/ws?src=1"
  );
  // const [messageHistory, setMessageHistory] = useState<MessageEvent<any>[]>([]);

  const { sendMessage, lastMessage, readyState, getWebSocket } = useWebSocket(
    url,
    { share: true, shouldReconnect: (closeEvent) => true }
  );

  const handleClickSendMessage = useCallback(
    () => sendMessage(JSON.stringify({ type: "mse" })),
    []
  );

  const connectionStatus = {
    [ReadyState.CONNECTING]: "Connecting",
    [ReadyState.OPEN]: "Open",
    [ReadyState.CLOSING]: "Closing",
    [ReadyState.CLOSED]: "Closed",
    [ReadyState.UNINSTANTIATED]: "Uninstantiated",
  }[readyState];

  useEffect(() => {
    if (getWebSocket() === null) {
      return;
    }
    //Change binaryType property of WebSocket
    if (readyState !== ReadyState.OPEN) {
      return;
    }
    // @ts-ignore
    getWebSocket().binaryType = "arraybuffer";
  }, [getWebSocket, readyState]);

  let mseCodecs = "";
  const buf = new Uint8Array(2 * 1024 * 1024);
  let bufLen = 0;

  useEffect(() => {
    if (lastMessage === null) return;
    if (msRef.current === null) return;
    if (getWebSocket() === null) return;
    // @ts-ignore
    if (getWebSocket().binaryType !== "arraybuffer") {
      // @ts-ignore
      getWebSocket().binaryType = "arraybuffer";
    }

    let ms: MediaSource = msRef.current;
    // let sb: SourceBuffer | null = null;

    const ev = lastMessage;
    if (typeof ev.data === "string") {
      const msg = JSON.parse(ev.data);
      if (msg.type === "mse") {
        const sb = ms.addSourceBuffer(msg.value);
        sb.mode = "segments"; // segments or sequence
        sb.addEventListener("updateend", () => {
          if (sb === null) return;
          if (sb.updating) return;
          try {
            if (bufLen > 0) {
              const data = buf.slice(0, bufLen);
              bufLen = 0;
              sb.appendBuffer(data);
            } else if (sb.buffered && sb.buffered.length) {
              const end = sb.buffered.end(sb.buffered.length - 1) - 15;
              const start = sb.buffered.start(0);
              if (end > start) {
                sb.remove(start, end);
                ms.setLiveSeekableRange(end, end + 15);
              }
            }
          } catch (e) {}
        });
      }
    } else {
      if (ms.sourceBuffers.length == 0) return;
      const sb = ms.sourceBuffers[0];
      if (sb === null || sb === undefined) return;
      const data = ev.data;
      if (sb.updating || bufLen > 0) {
        const b = new Uint8Array(data);
        buf.set(b, bufLen);
        bufLen += b.byteLength;
        console.debug("VideoRTC.buffer", b.byteLength, bufLen);
      } else {
        try {
          const b = new Uint8Array(data);
          sb.appendBuffer(b);
          console.debug("VideoRTC.buffer2", data);
        } catch (e) {
          console.debug(e);
        }
      }
    }
  }, [lastMessage]);

  const CODECS = [
    "avc1.640029", // H.264 high 4.1 (Chromecast 1st and 2nd Gen)
    "avc1.64002A", // H.264 high 4.2 (Chromecast 3rd Gen)
    "avc1.640033", // H.264 high 5.1 (Chromecast with Google TV)
    "hvc1.1.6.L153.B0", // H.265 main 5.1 (Chromecast Ultra)
    "mp4a.40.2", // AAC LC
    "mp4a.40.5", // AAC HE
    "flac", // FLAC (PCM compatible)
    "opus", // OPUS Chrome, Firefox
  ];

  const media = "video,audio";

  const codecs = (isSupported: (arg0: string) => unknown) => {
    return CODECS.filter(
      (codec) =>
        media.indexOf(codec.indexOf("vc1") > 0 ? "video" : "audio") >= 0
    )
      .filter((codec) => isSupported(`video/mp4; codecs="${codec}"`))
      .join();
  };

  const play = (video: HTMLVideoElement) => {
    video.play().catch(() => {
      if (!video.muted) {
        video.muted = true;
        video.play().catch((er) => {
          console.warn(er);
        });
      }
    });
  };

  const start = useCallback(
    (
      video: HTMLVideoElement,
      ms: MediaSource,
      siteId: number,
      appId: number,
      channelId: number,
      streamlId: number,
      liveOrRecording: boolean,
      timeStamp: number
    ) => {
      ms.addEventListener(
        "sourceopen",
        () => {
          URL.revokeObjectURL(video.src);
          sendMessage(
            JSON.stringify({
              type: "mse",
              value: codecs(MediaSource.isTypeSupported),
            })
          );
        },
        { once: true }
      );

      video.src = URL.createObjectURL(ms);
      video.srcObject = null;

      play(video);
      mseCodecs = "";
    },
    []
  );

  const stop = useCallback(
    (video: HTMLVideoElement | null, ms: MediaSource | null) => {
      if (video) {
        video.src = "";
        video.srcObject = null;
      }
      if (ms) {
        // ms.endOfStream();
      }
    },
    []
  );

  useEffect(() => {
    msRef.current = new MediaSource();

    if (videoRef?.current) {
      start(
        videoRef.current,
        msRef.current,
        debouncedSiteId,
        debouncedAppId,
        debouncedChannelId,
        debouncedStreamId,
        debouncedLiveOrRecording,
        debouncedTimeStamp
      );
    }
    return () => {
      stop(videoRef?.current, msRef.current);
    };
  }, []);

  return (
    <Box
      ref={boxRef}
      position={"relative"}
      sx={{
        aspectRatio: "16/9",
        // aspectRatio: "27.8/9",
        // aspectRatio: "23/9"
      }}>
      <Box>
        <Button>Click Me to change Socket Url</Button>
        <Button
          onClick={handleClickSendMessage}
          disabled={readyState !== ReadyState.OPEN}>
          Send
        </Button>
        <Text></Text>
      </Box>
      <Box
        as="video"
        ref={videoRef}
        position={"absolute"}
        disablePictureInPicture
        controls
        width={"100%"}
        height={"100%"}
        muted
        autoPlay={false}
        src="http://commondatastorage.googleapis.com/gtv-videos-bucket/sample/BigBuckBunny.mp4"
        style={{
          border: "2px solid blue",
        }}
      />
      <Box
        id="svg-container"
        as="svg"
        ref={svgRef}
        position={"absolute"}
        width={"100%"}
        height={"100%"}
        // zIndex={"1"}
        style={{
          border: "1px solid orange",
        }}
        viewBox={`0 0 ${videoWidth} ${videoHeight}`}
      />
    </Box>
  );
};

export default VideoPlayer;
