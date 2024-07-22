import { Box } from "@chakra-ui/react";
import { useRef, useState } from "react";
const VideoPlayer = () => {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const svgRef = useRef<SVGSVGElement | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const [videoWidth, setVideoWidth] = useState<number>(0);
  const [videoHeight, setVideoHeight] = useState<number>(0);

  return (
    <Box>
      <Box
        ref={boxRef}
        position={"relative"}
        sx={{
          aspectRatio: "16/9",
          // aspectRatio: "27.8/9",
          // aspectRatio: "23/9"
        }}>
        <Box
          id="svg-container"
          as="svg"
          ref={svgRef}
          position={"absolute"}
          width={"100%"}
          height={"100%"}
          zIndex={"1"}
          style={{
            border: "1px solid orange",
          }}
          viewBox={`0 0 ${videoWidth} ${videoHeight}`}></Box>
      </Box>
    </Box>
  );
};

export default VideoPlayer;
