import { Box } from "@chakra-ui/react";
import { useEffect, useRef, useState } from "react";
import { FrameInfo, ObjectInfo } from "../services/graphApiGen";

const REF_WIDTH = 1920;
const REF_HEIGHT = 1080;
export const SvgTest = () => {
  const svgRef = useRef<SVGSVGElement | null>(null);
  const [frameInfo, setFrameInfo] = useState<FrameInfo | null>(null);
  const [count, setCount] = useState(0);
  useEffect(() => {
    //Implementing the setInterval method
    const interval = setInterval(() => {
      if (count < REF_HEIGHT - 200) {
        setCount(count + 1);
      } else {
        setCount(0);
      }
    }, 10);

    //Clearing the interval
    return () => clearInterval(interval);
  }, [count]);

  useEffect(() => {
    setFrameInfo({
      siteId: 1,
      channelId: 0,
      timeStamp: 0,
      timeStampEnd: 0,
      vehicleCount: 0,
      peopleCount: 0,
      refWidth: REF_WIDTH,
      refHeight: REF_HEIGHT,
      objectList: [
        { x: 100, y: count, w: 100, h: 200, t: 0, c: 10, i: 0 },
      ] as ObjectInfo[],
    } as FrameInfo);
  }, [count]);
  return (
    <Box
      as="svg"
      ref={svgRef}
      style={{
        border: "1px solid orange",
      }}
      viewBox={`0 0 ${REF_WIDTH} ${REF_HEIGHT}`}>
      <g>
        {frameInfo?.objectList?.map((obj, index) => {
          // console.log(
          //   frameInfo.refWidth,
          //   frameInfo.refHeight,
          //   obj.x,
          //   obj.y,
          //   obj.w,
          //   obj.h,
          //   (obj.x * REF_WIDTH) / frameInfo.refWidth,
          //   (obj.y * REF_HEIGHT) / frameInfo.refHeight,
          //   (obj.w * REF_WIDTH) / frameInfo.refWidth,
          //   (obj.h * REF_HEIGHT) / frameInfo.refHeight
          // );

          return (
            <rect
              key={index}
              x={(obj.x * REF_WIDTH) / frameInfo.refWidth!}
              y={(obj.y * REF_HEIGHT) / frameInfo.refHeight!}
              width={(obj.w * REF_WIDTH) / frameInfo.refWidth!}
              height={(obj.h * REF_HEIGHT) / frameInfo.refHeight!}
              fillOpacity={0}
              stroke={"red"}
              strokeWidth={5}
            />
          );
        })}
      </g>
    </Box>
  );
};
