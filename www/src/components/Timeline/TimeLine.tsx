// https://vizhub.com/curran/32dfc8d2393844c6a5b9d199d9a35946?f90a6c7a=ta
// https://www.youtube.com/watch?v=5bPF-lTvs5E&list=RDCMUCSwd_9jyX4YtDYm9p9MxQqw&index=14
// https://observablehq.com/@d3/d3-scaletime
// https://codesandbox.io/p/sandbox/quirky-yalow-3y6q4?file=%2Fsrc%2FcustomChart%2FLineChart.jsx%3A28%2C62
import { Alert, AlertIcon, Box, Skeleton } from "@chakra-ui/react";
import { useSize } from "@chakra-ui/react-use-size";
import {
  ZoomTransform,
  reduce,
  scaleBand,
  scaleTime,
  select,
  timeFormat,
  zoom,
} from "d3";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import AxisBottom from "./AxisBottom";
import VideoAvilable from "./VideoAvilable";
import HumanType from "./HumanType";
import VehicleType from "./VehicleType";
import BandwidthType from "./BandwidthType";
import {
  MetadataBar,
  useGetMetadataBarsQuery,
  useSeekStreamMutation,
} from "../../services/graphApiGen";
import { max, min } from "d3";
import InitialAxisBottom from "./InitialAxisBottom";
import InitialCircle from "./InitialCircle";
type Margin = {
  top: number;
  bottom: number;
  left: number;
  right: number;
};
const margin: Margin = {
  top: 20,
  bottom: 3,
  left: 25,
  right: 20,
};
const theme = {
  pixelsPerTick: 50,
};
interface Timelines {
  siteId: number;
  channelId: number;
  startTime:number;
  endTime:number;
  heightFromParent?: number;
  timeStamp: number;
  onDataSend: (
    clickedTimestamp: string,
    drag: boolean,
    dragStart: number
  ) => void;
  currentTime: number;
  liveOrRecording: boolean;
  ready: boolean;
  sessionId: string;
  appId: number;
  showTimeline: boolean;
}

const TimeLine = ({
  siteId,
  channelId,
  heightFromParent,
  timeStamp,
  onDataSend,
  currentTime,
  liveOrRecording,
  ready,
  sessionId,
  appId,
  showTimeline,
  startTime,endTime
}: Timelines) => {
  const targetRef = useRef<HTMLDivElement | null>(null);
  const svgRef = useRef<SVGSVGElement | null>(null);
  const { width, height } = useSize(targetRef) ?? {
    width: 10,
    height: 10,
  };
  const { innerWidth, innerHeight } = useMemo(() => {
    return {
      innerWidth: Math.floor(width - margin.left - margin.right),
      innerHeight: Math.floor(height - margin.top - margin.bottom),
    };
  }, [width, height]);
  const countRef = useRef(0);
  // const [startTime, setStartTime] = useState<number>(0);
  const [dragStart, setDragStart] = useState<number>(0);
  // const [endTime, setEndTime] = useState<number>(0);
  const { data, isError, isLoading, isFetching } = useGetMetadataBarsQuery({
    siteId: siteId,
    channelId: channelId,
    startTime: startTime,
    endTime: endTime,
  });
  const [seekSdp] = useSeekStreamMutation();
  const handleSeek = async (SeekTime: number) => {
    {
      if (!liveOrRecording)
        try {
          const seekRes = await seekSdp({
            sessionId: sessionId,
            siteId: siteId,
            channelId: channelId,
            appId: appId,
            timeStamp: SeekTime,
          });
        } catch (error) {
          console.log("Error during mutation:", error);
        }
    }
  };
  useEffect(() => {
    handleSeek(startTime);
  }, [dragStart]);
  // useEffect(() => {
  //   const now = new Date();
  //   const oneHourAgo = new Date(now.getTime() - 1 * 60 * 60 * 1000);
  //   setStartTime(oneHourAgo.getTime());
  //   setEndTime(now.getTime());
  // }, []);
  // const [drag, setDrag] = useState<boolean>(false);
  // useEffect(() => {
  //   if (currentTime > endTime + 5000 && drag === false) {
  //     const now = new Date();
  //     const oneSecAgo = new Date(now.getTime() - 1 * 60 * 60 * 1000);
  //     setStartTime(oneSecAgo.getTime());
  //     setEndTime(now.getTime());
  //   }
  // }, [currentTime]);

  // useEffect(() => {
  //   if (liveOrRecording) {
  //     const now = new Date();
  //     const oneHourAgo = new Date(now.getTime() - 1 * 60 * 60 * 1000);
  //     setStartTime(oneHourAgo.getTime());
  //     setEndTime(now.getTime());
  //   }
  // }, [liveOrRecording]);

  // useEffect(() => {
  //   if (
  //     !liveOrRecording &&
  //     currentTime > endTime + 1 &&
  //     currentTime < endTime + 1000
  //   ) {
  //     setStartTime(currentTime - 30 * 60 * 1000);
  //     setEndTime(currentTime + 30 * 60 * 1000);
  //   }
  // }, [currentTime]);

  const yScale = scaleBand<string>()
    .domain(["human", "bandwidth", "vehicle", "video"])
    .range([margin.top, innerHeight])
    .padding(0.2);
  const getStartOfDate = useCallback((date: Date): Date => {
    let result = new Date(date);
    result.setHours(0, 0, 0, 0);
    return result;
  }, []);
  const getEndOfDate = useCallback((date: Date): Date => {
    let result = new Date(date);
    result.setHours(23, 59, 59, 999);
    return result;
  }, []);
  const getStartOfMonth = useCallback((date: Date): Date => {
    let result = new Date(date);
    result.setHours(0, 0, 0, 0);
    result.setDate(1);
    return result;
  }, []);
  const getLastNDays = useCallback((date: Date, n: number): Date => {
    let result = new Date(date);
    result.setHours(0, 0, 0, 0);
    result.setDate(result.getDate() - n);
    return result;
  }, []);
  const [xDomainMin, setXDomainMin] = useState<Date>(
    getLastNDays(new Date(), 1)
  );
  const [xDomainMax, setXDomainMax] = useState<Date>(getEndOfDate(new Date()));
  const [mainData, setMainData] = useState<MetadataBar>();
  const xScale = useMemo(() => {
    return scaleTime()
      .domain([xDomainMin, xDomainMax])
      .rangeRound([margin.left, innerWidth])
      .nice();
  }, [xDomainMin, xDomainMax, margin.left, innerWidth]);
  const numberOfTicksTarget = useMemo(() => {
    Math.max(1, Math.floor(innerWidth / theme.pixelsPerTick));
  }, [innerWidth]);
  // const xScaleOriginal = useMemo(() => {
  //   if (data)
  //     return scaleTime()
  //       .domain([xDomainMin, xDomainMax])
  //       .rangeRound([margin.left, innerWidth])
  //       .nice();
  // }, [xDomainMin, xDomainMax, margin.left, innerWidth]);
  const xScaleOriginal = useMemo(() => {
    if (data) {
      // console.log(data);
      const startTimeArray = data.humanBars
        .map((bar) => bar.startTime)
        .filter((time) => time !== undefined) as number[];
      const endTimeArray = data.humanBars
        .map((bar) => bar.endTime)
        .filter((time) => time !== undefined) as number[];
      return scaleTime()
        .domain([data.startTime!, data.endTime!])
        .rangeRound([margin.left, innerWidth])
        .nice();
    }
  }, [data, innerWidth, margin.left, xDomainMax, xDomainMin, startTime]);
  const [dataLength, setDataLength] = useState<number>(data?.humanBars.length!);

  useEffect(() => {
    setDataLength(data?.humanBars.length!);
  }, [data?.humanBars.length!]);
  useEffect(() => {
    if (data) {
      setMainData(data);
      // const xMin = min(data.humanBars.map((bar) => bar.startTime));
      // const xMax = max(data.humanBars.map((bar) => bar.endTime));
      const xMin = data.startTime;
      const xMax = data.endTime;
      if (xMin && xMax) {
        setXDomainMin(new Date(xMin));
        setXDomainMax(new Date(xMax));
      }
    }
    // extent(data.map((d) => d.date)) as [Date, Date]
  }, [data, startTime]);
  // useEffect(() => {
  //   if (data) {
  //     setMainData(data);
  //     const xMin = data.startTime;
  //     const xMax = data.endTime;

  //     if (xMin && xMax) {
  //       setXDomainMax(new Date(xMax));
  //       const adjustedStartTime = startTime - 3600 * 1000; // Subtract 1 hour in milliseconds

  //       if (adjustedStartTime >= xMin) {
  //         setStartTime(adjustedStartTime);
  //       }
  //     }
  //   }
  // }, [data]);
  // Adjust xDomainMin outside of useEffect to reflect the updated start time

  useEffect(() => {
    let prevMouseX = 0;

    const handleMouseMove = (event: MouseEvent) => {
      const currentMouseX = event.clientX;

      if (currentMouseX > prevMouseX) {
        consecutiveXValueCountRef.current++;
      } else {
        consecutiveXValueCountRef.current = 0;
      }

      prevMouseX = currentMouseX;
    };

    document.addEventListener("mousemove", handleMouseMove);

    return () => {
      document.removeEventListener("mousemove", handleMouseMove);
    };
  }, []);
  useEffect(() => {
    setXDomainMin(new Date(startTime));
  }, [startTime]);
  const [isLeftEndReached, setIsLeftEndReached] = useState(false);
  const consecutiveXValueCountRef = useRef(0);
  const prevXValueRef = useRef<number | null>(null);
  useEffect(() => {
    const selection = select(svgRef.current);
    selection.call(
      zoom()
        .scaleExtent([1 / 2, 50])
        .translateExtent([
          [margin.left, margin.top],
          [innerWidth, innerHeight],
        ])
        .on("zoom", (event: { transform: ZoomTransform }) => {
          const { transform } = event;
          // console.log(transform.x);
          if (!transform) return;
          const newXScale = transform.rescaleX(xScaleOriginal!);
          const d = newXScale.domain();
          // console.log("d:", d);

          setXDomainMin(d[0]);
          setXDomainMax(d[1]);
          if (xScaleOriginal && d[0].getTime() <= Number(data?.startTime)) {
            setIsLeftEndReached(true);
            // console.log("Left end reached!");
          } else {
            setIsLeftEndReached(false);
          }
          const currentXValue = transform.x;
          if (prevXValueRef.current !== null) {
            if (currentXValue === prevXValueRef.current) {
              consecutiveXValueCountRef.current++;
            } else {
              consecutiveXValueCountRef.current = 0;
            }
          }

          prevXValueRef.current = currentXValue;

          // console.log(isLeftEndReached);
          if (consecutiveXValueCountRef.current === 8) {
            const adjustedStartTime = startTime - 3600 * 1000;
            const adjustedEndTime = endTime - 3600 * 1000;
            // setDrag(true);
            // setStartTime(adjustedStartTime);
            // setDragStart(adjustedStartTime);
            // setEndTime(adjustedEndTime);
            handleSeek(adjustedStartTime);
            consecutiveXValueCountRef.current = 0;
          }
        }) as any
    );
    selection.on("dblclick.zoom", null); //To disable the default double click
    return () => {
      selection.on("zoom", null);
    };
  }, [innerWidth, innerHeight, xScaleOriginal, xDomainMax, xDomainMin]);

  const [clickedTimestamp, setClickedTimestamp] = useState<string | null>(null);
  const handleChartClick = useCallback(
    (event: React.MouseEvent<SVGSVGElement, MouseEvent>) => {
      const mouseX =
        event.clientX - event.currentTarget.getBoundingClientRect().left;
      const clickedTime = xScale.invert(mouseX);
      console.log("Clicked time:", clickedTime);
      setClickedTimestamp(timeFormat("%c")(clickedTime));
    },
    [xScale]
  );

  // useEffect(() => {
  //   onDataSend(clickedTimestamp!, drag, dragStart);
  // }, [clickedTimestamp, drag, dragStart]);

  return isError ? (
    <Alert status="error">
      <AlertIcon />
      SERVER_ERROR
    </Alert>
  ) : (
    <Box ref={targetRef} m={0} p={0} height={heightFromParent ?? "100%"}>
      {isFetching ? (
        <Alert status="warning">
          <AlertIcon />
          FETCHING_DATA
        </Alert>
      ) : (
        <Box
          as="svg"
          ref={svgRef}
          width={"100%"}
          height={"100%"}
          style={{
            border: "1px solid orange",
            display: showTimeline ? "block" : "none",
          }}
          onClick={handleChartClick}>
          {dataLength > 0 && ready ? (
            <AxisBottom
              xScale={xScale}
              innerHeight={innerHeight}
              innerWidth={innerWidth}
              marginLeft={margin.left}
            />
          ) : (
            <InitialAxisBottom
              innerHeight={innerHeight}
              innerWidth={innerWidth}
              marginLeft={margin.left}
            />
          )}
          {dataLength > 0 && ready ? (
            <VideoAvilable
              data={data?.humanBars}
              xScale={xScale}
              yScale={yScale}
              marginLeft={margin.left}
              marginRight={margin.right}
              innerWidth={innerWidth}
              innerHeight={innerHeight}
              mainData={mainData}
              timeStamp={timeStamp}
              currentTimes={currentTime}
            />
          ) : (
            <InitialCircle
              data={data?.humanBars}
              xScale={xScale}
              yScale={yScale}
              marginLeft={margin.left}
              marginRight={margin.right}
              innerWidth={innerWidth}
              innerHeight={innerHeight}
              mainData={mainData}
              timeStamp={timeStamp}
              currentTimes={currentTime}
              dataLength={dataLength}
            />
          )}

          <HumanType
            data={data?.humanBars}
            xScale={xScale}
            yScale={yScale}
            marginLeft={margin.left}
            innerWidth={innerWidth}
            innerHeight={innerHeight}
            marginRight={margin.right}
          />
          <VehicleType
            data={data?.vehicleBars}
            xScale={xScale}
            yScale={yScale}
            marginLeft={margin.left}
            innerWidth={innerWidth}
            innerHeight={innerHeight}
            marginRight={margin.right}
          />
          {/* <BandwidthType
          data={data?.vehicleBars}
          xScale={xScale}
          yScale={yScale}
          marginLeft={margin.left}
          innerWidth={innerWidth}
          innerHeight={innerHeight}
          marginRight={margin.right}
        /> */}
          {/* <AllType data={allType}
          xScale={xScale}
          yScale={yScale}/> */}
        </Box>
      )}
    </Box>
  );
};
export default TimeLine;
