import { ScaleTime } from "d3";
import { useColorModeValue, useTheme } from "@chakra-ui/react";
import { Bar } from "../../services/graphApiGen";

interface TimelineRectsProps {
  data: Bar[] | undefined;
  xScale: ScaleTime<number, number>;
  yScale: any;
  marginLeft: number;
  innerWidth: number;
  innerHeight: number;
  marginRight: number;
}

const BandwidthType = ({
  data,
  xScale,
  innerHeight,
  marginLeft,
  innerWidth,
  marginRight,
}: TimelineRectsProps) => {
  const theme = useTheme();
  const barColor = useColorModeValue(
    theme.colors.yellow[400],
    theme.colors.yellow[300]
  );
  const borderColor = useColorModeValue(
    theme.colors.yellow[800],
    theme.colors.yellow[100]
  );
  const lineColor = useColorModeValue(
    theme.colors.gray[600],
    theme.colors.gray[300]
  );

  const themes = {
    h1: 80,
    y1: 200,
  };

  const adjustedH1 = (innerHeight / 500) * themes.h1;

  const adjustedY = (innerHeight / 500) * themes.y1;

  // const timeFormatter = timeFormat("%Y-%m-%d %H:%M:%S");
  // const handleRectClick = (event: React.MouseEvent<SVGRectElement>) => {
  //   const xCoord = event.nativeEvent.offsetX;
  //   const invertedDate = xScale.invert(xCoord);
  //   console.log("From bandwidthType component", timeFormatter(invertedDate));
  // };
  return (
    <g>
      {data?.map((item, index) => {
        const xStart = xScale(item.startTime);
        const xEnd = xScale(item.endTime);
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
            stroke={borderColor}
            strokeWidth={1}
            // onClick={(event) => handleRectClick(event)}
          />
        );
      })}
      <line
        x1={0}
        x2={innerWidth + marginRight + marginLeft}
        y1={innerHeight - adjustedY}
        y2={innerHeight - adjustedY}
        stroke={lineColor}
        strokeWidth={2}
        opacity={0.2}
      />
    </g>
  );
};

export default BandwidthType;

// import React from "react";
// import { ScaleTime } from "d3";

// interface TimelineRectsProps {
//   data: { startTime: Date; endTime: Date; type: string }[];
//   xScale: ScaleTime<number, number>;
//   yScale: any;
// }
// const getColorByType = (type: string)=> {
//     switch (type) {
//       case "bandwidth":
//         return "orange";
//       default:
//          null;
//     }
//   };

// const BandwidthType: React.FC<TimelineRectsProps> = ({ data, xScale, yScale }) => {
//     const bandwidthData = data.filter((item) => item.type === "bandwidth");
//   return (

//     <g>
//       {bandwidthData.map((item, index) => {
//         const xStart = xScale(item.startTime);
//         const xEnd = xScale(item.endTime);
//         const y = yScale("bandwidth");

//         return (
//           <rect
//             rx={5}
//             key={index}
//             x={xStart}
//             y={y}
//             width={xEnd - xStart}
//             height={yScale.bandwidth()}
//             fill={getColorByType(item.type)}
//             opacity={0.7}
//           />
//         );
//       })}
//     </g>
//   );
// };
// export default BandwidthType;
