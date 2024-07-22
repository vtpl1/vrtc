import React, { useEffect, useState } from "react";
import { FrameInfo } from "../services/graphApiGen";
import { format, formatDistance, formatRelative, subDays } from 'date-fns'
interface FrameOverlayProps {
  frameInfo: FrameInfo | null;
  REF_WIDTH: number;
  REF_HEIGHT: number;
  liveOrRecording: boolean;
  currentTime: number;
  channelId: number;
}

const FrameInfos: React.FC<FrameOverlayProps> = ({
  frameInfo,
  REF_WIDTH,
  REF_HEIGHT,
  liveOrRecording,
  currentTime,
  channelId,
}) => {
  const [boxWidth, setBoxWidth] = useState<number>(0);
  const [boxHeight, setBoxHeight] = useState<number>(0);
  // const[eventTimes,setEventTimes]=useState<number>(eventTime)
  // useEffect(()=>{
  //   if(eventTime)
  //     setEventTimes(eventTime)
  // },[eventTime])
  // const trackIds: string[] =
  //   frameInfo?.objectList?.map((obj) => String(obj.i)) || [];
  // const cars = new Array("2", "5", "7");

  // console.log(cars)

  // const { data, isError, isLoading, isFetching } = useGetAttributeEventsQuery({
  //   channelId: channelId,
  //   startTime: currentTime - 75000,
  //   endTime: currentTime + 75000,
  //   trackId: trackIds,
  // });

  // useEffect(()=>{
  //   if(data)
  //     // console.log(data)
  // },[currentTime])
  useEffect(() => {
    const svgElement = document.getElementById("svg-container");
    if (svgElement) {
      const rect = svgElement.getBoundingClientRect();
      setBoxWidth(rect.width);
      setBoxHeight(rect.height);
      // console.log("width ", boxWidth);
      // console.log("height ", boxHeight);
    }
  }, [frameInfo]);
  const human = 1;
  const vehicle = 1;
  return (
    <g>
      {frameInfo?.objectList?.map((obj, index) => {
        // console.log( Math.sqrt(obj.w * obj.h) *
        // Math.sqrt(REF_WIDTH * REF_HEIGHT) /
        // Math.sqrt(frameInfo.refWidth * frameInfo.refHeight))
        // const metaAttributeEvent = data?.metaAttributeEvent.find(
        //   (event) => event.trackid === obj.i
        // );
        // const bottomColor = metaAttributeEvent?.bottomcolor || "u";

        if (obj.t === 1 || obj.t === 2)
          return (
            <React.Fragment key={index}>
              <rect
                rx={10}
                x={(obj.x * REF_WIDTH) / frameInfo.refWidth!}
                y={(obj.y * REF_HEIGHT) / frameInfo.refHeight!}
                width={(obj.w * REF_WIDTH) / frameInfo.refWidth!}
                height={(obj.h * REF_HEIGHT) / frameInfo.refHeight!}
                fillOpacity={0}
                stroke={obj.t === human ? "#ff595e" : "#8ac926"}
                strokeWidth={5}
                opacity={1}
              />
              <rect
                rx={10}
                x={(obj.x * REF_WIDTH) / frameInfo.refWidth!}
                y={
                  (obj.y * REF_HEIGHT) / frameInfo.refHeight! +
                  (obj.y < 35
                    ? (obj.h * REF_HEIGHT) / frameInfo.refHeight! - 30
                    : -31)
                }
                height={30}
                // width={(obj.w * REF_WIDTH) / frameInfo.refWidth}
                width={
                  boxWidth < 547.5
                    ? 80
                    : (Math.sqrt(obj.w * obj.h) *
                      Math.sqrt(REF_WIDTH * REF_HEIGHT)) /
                      Math.sqrt(
                        frameInfo.refWidth! * frameInfo.refHeight!
                      ) <
                      130
                      ? 65
                      : 70
                }
                // stroke={`url(#gradient-${index})`}
                stroke={obj.t === human ? "#ff595e" : "#8ac926"}
                // fill={`url(#gradient-${index})`}
                fill={obj.t === human ? "#ff595e" : "#8ac926"}
                strokeWidth={5}
                opacity={0.7}></rect>

              <text
                x={(obj.x * REF_WIDTH) / frameInfo.refWidth! + 3}
                // y={(obj.y * REF_HEIGHT) / frameInfo.refHeight + 25}
                y={
                  (obj.y * REF_HEIGHT) / frameInfo.refHeight! +
                  (obj.y < 35
                    ? (obj.h * REF_HEIGHT) / frameInfo.refHeight! - 10
                    : -10)
                }
                fill="white"
                fontSize={
                  boxWidth < 547.5
                    ? 35
                    : (Math.sqrt(obj.w * obj.h) *
                      Math.sqrt(REF_WIDTH * REF_HEIGHT)) /
                      Math.sqrt(
                        frameInfo.refWidth! * frameInfo.refHeight!
                      ) <
                      130
                      ? 28
                      : 30
                }
                fontFamily="Calibri">
                {obj.t === human ? `H -${format(frameInfo.timeStamp, "pp")}` : `V -${format(frameInfo.timeStamp, "pp")}`}
              </text >
            </React.Fragment >
          );
      })}
    </g >
  );
};

export default FrameInfos;
