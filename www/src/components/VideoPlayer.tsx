import { Box } from "@chakra-ui/react";
import { useRef, useState } from "react";
import React, { useCallback, useEffect } from "react";
import useWebSocket, { ReadyState } from "react-use-websocket";
const VideoPlayer = () => {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const svgRef = useRef<SVGSVGElement | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const [videoWidth, setVideoWidth] = useState<number>(0);
  const [videoHeight, setVideoHeight] = useState<number>(0);
  const [socketUrl, setSocketUrl] = useState("wss://echo.websocket.org");
  const [messageHistory, setMessageHistory] = useState<MessageEvent<any>[]>([]);

  const { sendMessage, lastMessage, readyState } = useWebSocket(socketUrl);

  useEffect(() => {
    if (lastMessage !== null) {
      setMessageHistory((prev) => prev.concat(lastMessage));
    }
  }, [lastMessage]);

  const handleClickChangeSocketUrl = useCallback(
    () => setSocketUrl("wss://demos.kaazing.com/echo"),
    []
  );

  const handleClickSendMessage = useCallback(() => sendMessage("Hello"), []);

  const connectionStatus = {
    [ReadyState.CONNECTING]: "Connecting",
    [ReadyState.OPEN]: "Open",
    [ReadyState.CLOSING]: "Closing",
    [ReadyState.CLOSED]: "Closed",
    [ReadyState.UNINSTANTIATED]: "Uninstantiated",
  }[readyState];

  return (
    <Box
      ref={boxRef}
      position={"relative"}
      sx={{
        aspectRatio: "16/9",
        // aspectRatio: "27.8/9",
        // aspectRatio: "23/9"
      }}>
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
