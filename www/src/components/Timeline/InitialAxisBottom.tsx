import { useColorModeValue, useTheme } from "@chakra-ui/react";
import { ScaleTime, scaleTime, timeFormat } from "d3";
import { useMemo, useState } from "react";

interface XAxisProps {
  innerHeight: number;
  innerWidth: number;
  marginLeft: number;
}

function AxisBottom({ innerHeight, innerWidth, marginLeft }: XAxisProps) {
  const theme = useTheme();
  const textColor = useColorModeValue(
    theme.colors.gray[600],
    theme.colors.gray[300]
  );
  const now = new Date();
  const xDomainMin = new Date(now.getTime() - 1 * 60 * 60 * 1000);
  const xDomainMax = new Date();
  const xScale = useMemo(() => {
    return scaleTime()
      .domain([xDomainMin, xDomainMax])
      .rangeRound([25, innerWidth])
      .nice();
  }, [xDomainMin, xDomainMax, 25, innerWidth]);
  const timeFormatter2 = xScale.tickFormat();
  const themes = {
    pixelsPerTick: 70,
    subTicks: 10,
    tickY1: 305,
    tickY2: 275,
    subTickY1: 305,
    subTickY2: 275,
    textHeight: 320,
  };
  const adjustedTickHeight1 = (innerHeight / 500) * themes.tickY1;
  const adjustedTickHeight2 = (innerHeight / 500) * themes.tickY2;
  const adjustedSubTickHeight1 = (innerHeight / 500) * themes.subTickY1;
  const adjustedSubTickHeight2 = (innerHeight / 500) * themes.subTickY2;
  const adjustedTextHeight = (innerHeight / 500) * themes.textHeight;

  const numberOfTicksTarget = useMemo(() => {
    return Math.max(
      1,
      Math.floor((innerWidth - marginLeft) / themes.pixelsPerTick)
    );
  }, [innerWidth, themes.pixelsPerTick, marginLeft]);

  const ticks = useMemo(() => {
    const domain = xScale.domain();
    const tickValues = xScale.ticks(numberOfTicksTarget);
    return tickValues.filter(
      (value) => domain[0] <= value && value <= domain[1]
    );
  }, [xScale, numberOfTicksTarget]);

  const subticks = useMemo(() => {
    const subticksArray: Date[] = [];
    ticks.forEach((tickValue, index, array) => {
      if (index < array.length - 1) {
        const nextTickValue = array[index + 1];
        const subtickInterval =
          (nextTickValue.getTime() - tickValue.getTime()) / themes.subTicks;
        for (let i = 1; i < themes.subTicks; i++) {
          subticksArray.push(
            new Date(tickValue.getTime() + i * subtickInterval)
          );
        }
      }
    });
    return subticksArray;
  }, [ticks, themes.subTicks]);

  return (
    <>
      {ticks.map((tickValue: Date, index: number) => (
        <g
          className="x-axis"
          key={`${tickValue.getTime()}-${index}`}
          transform={`translate(${xScale(tickValue)},0)`}>
          <line
            y1={innerHeight - adjustedTickHeight1}
            y2={innerHeight - adjustedTickHeight2}
            stroke={textColor}
            strokeWidth="1"
          />
          <text
            key={`label-${tickValue.getTime()}`}
            y={innerHeight - adjustedTextHeight}
            textAnchor="middle"
            style={{ fontSize: "10px", fill: textColor }}>
            {timeFormatter2(tickValue)}
          </text>
        </g>
      ))}
      {subticks.map((subtickValue: Date, index: number) => (
        <g
          className="x-axis subtick"
          key={`${subtickValue.getTime()}-${index}`}
          transform={`translate(${xScale(subtickValue)},0)`}>
          <line
            y1={innerHeight - adjustedSubTickHeight1}
            y2={innerHeight - adjustedSubTickHeight2}
            stroke={textColor}
            strokeWidth="0.5"
          />
        </g>
      ))}
    </>
  );
}

export default AxisBottom;
