import { ScaleTime, max, min, scaleTime } from "d3";
import { useColorModeValue, useTheme } from "@chakra-ui/react";
import { Bar, MetadataBar } from "../../services/graphApiGen";
import { useEffect, useMemo, useState } from "react";

interface TimelineRectsProps {
  mainData: any;
  data: Bar[] | undefined;
  xScale: ScaleTime<number, number>;
  yScale: any;
  marginLeft: number;
  marginRight: number;
  innerWidth: number;
  innerHeight: number;
  timeStamp: number;
  currentTimes: number;
}
const TimelineRects = ({
  mainData,
  data,
  xScale,
  marginLeft,
  innerWidth,
  innerHeight,
  timeStamp,
  currentTimes,
}: TimelineRectsProps) => {
  const theme = useTheme();
  const barColor = useColorModeValue(
    theme.colors.blue[600],
    theme.colors.blue[200]
  );
  const borderColor = useColorModeValue(
    theme.colors.blue[800],
    theme.colors.blue[100]
  );
  const textColor = useColorModeValue(
    theme.colors.gray[600],
    theme.colors.gray[300]
  );
  const themes = {
    h1: 80,
    y1: 490,
  };

  const adjustedH1 = (innerHeight / 500) * themes.h1;
  const adjustedY = (innerHeight / 500) * themes.y1;
  // const maxEndTime = max(validEndTimes, (d) => d.endTime);
  const Time = mainData?.endTime;
  const [currentTime, setCurrentTime] = useState<number>(Time);

  const circleX = xScale(currentTime);
  const circleRadius = adjustedH1 / 1.5 >= 0 ? adjustedH1 / 1.5 : 0;

  useEffect(() => {
    setCurrentTime(timeStamp);
    // console.log("timeStamp:", timeStamp);
  }, [timeStamp]);

  useEffect(() => {
    setCurrentTime(currentTimes);
    // console.log("currentTime:",currentTimes);
  }, [currentTimes]);

  return (
    <g>
      {/* {data?.map((item, index) => {
        const xStart = xScale.nice()(item.startTime);
        const xEnd = xScale.nice()(item.endTime);
        const y = innerHeight - adjustedY;
        const width = Math.min(innerWidth, xEnd) - Math.max(marginLeft, xStart);
        const rectWidth = width >= 0 ? width : 0;
        return (
          <rect
            rx={5}
            key={index}
            x={Math.max(marginLeft, xStart)}
            y={y}
            width={rectWidth}
            height={adjustedH1}
            fill={barColor}
            opacity={0.5}
            // stroke={borderColor}
            // strokeWidth={2}
          />
        );
      })} */}
      <circle
        // cx={innerWidth / 2}
        cx={circleX}
        cy={innerHeight - adjustedY + adjustedH1 / 2}
        r={circleRadius}
        fill={barColor}
        opacity={0.7}
        stroke={borderColor}
        strokeWidth={2}
      />
      <line
        // x1={innerWidth / 2}
        x1={circleX}
        y1={innerHeight - adjustedY + adjustedH1 / 2 + adjustedH1 / 1.5}
        // x2={innerWidth / 2}
        x2={circleX}
        y2={innerHeight + 10}
        stroke={textColor}
        strokeWidth={2}
        opacity={0.7}
      />
    </g>
  );
};
export default TimelineRects;
