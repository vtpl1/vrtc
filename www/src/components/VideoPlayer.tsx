import { Box, Button, Text } from "@chakra-ui/react";
import { useRef, useState, useCallback, useEffect } from "react";
import { useDebounce } from "@uidotdev/usehooks";



const VideoPlayer = () => {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const svgRef = useRef<SVGSVGElement | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const [videoWidth, setVideoWidth] = useState<number>(0);
  const [videoHeight, setVideoHeight] = useState<number>(0);



  // const handleClickSendMessage = useCallback(() => sendJsonMessage({ "type": "mse" }), []);


  const [siteId, setSiteId] = useState<number>(-1);
  const [channelId, setChannelId] = useState<number>(-1);
  const [appId, setAppId] = useState<number>(0);
  const [liveOrRecording, setLiveOrRecording] = useState<boolean>(true);
  const [streamId, setStreamId] = useState<number>(0);
  const [timeStamp, setTimeStamp] = useState<number>(-1);

  const debouncedSiteId = useDebounce(siteId, 500)
  const debouncedChannelId = useDebounce(channelId, 500)
  const debouncedAppId = useDebounce(appId, 500)
  const debouncedLiveOrRecording = useDebounce(liveOrRecording, 500)
  const debouncedStreamId = useDebounce(streamId, 500)
  const debouncedTimeStamp = useDebounce(timeStamp, 500)


  const start = useCallback(
    (
      video: HTMLVideoElement,
      siteId: number,
      appId: number,
      channelId: number,
      streamlId: number,
      liveOrRecording: boolean,
      timeStamp: number
    ) => { }, [])
  const stop = useCallback(
    (
      video: HTMLVideoElement | null
    ) => {
      if (video) {
        video.src = ""
        video.srcObject = null
      }
    }, [])
  useEffect(() => {

    if (videoRef?.current) {
      start(videoRef.current, debouncedSiteId, debouncedAppId, debouncedChannelId, debouncedStreamId, debouncedLiveOrRecording, debouncedTimeStamp)
    }
    return () => {
      stop(videoRef?.current)
    }
  }, [])
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
        <Button >
          Click Me to change Socket Url
        </Button>
        <Button >
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
