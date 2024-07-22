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
  dataLength: number;
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
  dataLength,
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
  const now = new Date();
  const xDomainMin = new Date(now.getTime() - 1 * 60 * 60 * 1000);
  const xDomainMax = new Date();
  const xScaleInvalid = useMemo(() => {
    return scaleTime()
      .domain([xDomainMin, xDomainMax])
      .rangeRound([25, innerWidth])
      .nice();
  }, [xDomainMin, xDomainMax, 25, innerWidth]);
  const adjustedH1 = (innerHeight / 500) * themes.h1;
  const adjustedY = (innerHeight / 500) * themes.y1;
  // const maxEndTime = max(validEndTimes, (d) => d.endTime);
  const Time = mainData?.endTime;
  const [currentTime, setCurrentTime] = useState<number>(Time);

  const circleX = xScale(currentTime);
  const circleXInvalid = xScaleInvalid(new Date());
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
      <circle
        cx={circleXInvalid}
        cy={innerHeight - adjustedY + adjustedH1 / 2}
        r={circleRadius}
        fill={barColor}
        opacity={0.7}
        stroke={borderColor}
        strokeWidth={2}
      />
      <line
        x1={circleXInvalid}
        y1={innerHeight - adjustedY + adjustedH1 / 2 + adjustedH1 / 1.5}
        x2={circleXInvalid}
        y2={innerHeight + 10}
        stroke={textColor}
        strokeWidth={2}
        opacity={0.7}
      />
    </g>
  );
};
export default TimelineRects;
