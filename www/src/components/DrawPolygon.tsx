import { Box } from "@chakra-ui/react";
import { useEffect, useState } from "react";

interface Coordinates {
  x: number;
  y: number;
}

interface DrawZoneProps {
  videoRef: any;
  REF_HEIGHT: number;
  REF_WIDTH: number;
  svgRef: any;
  isDrawing: boolean;
}

const DrawZone = ({
  videoRef,
  REF_HEIGHT,
  REF_WIDTH,
  svgRef,
  isDrawing,
}: DrawZoneProps) => {
  const [polygons, setPolygons] = useState<Coordinates[][]>([]);
  const [currentPolygon, setCurrentPolygon] = useState<Coordinates[]>([]);
  const [draggedPointIndex, setDraggedPointIndex] = useState<{
    polygonIndex: number;
    pointIndex: number;
  } | null>(null);

  const isPolygonClosed = (): boolean => {
    if (currentPolygon.length < 3) {
      return false;
    }

    const lastPoint = currentPolygon[currentPolygon.length - 1];
    const startPoint = currentPolygon[0];

    return (
      Math.abs(lastPoint.x - startPoint.x) <= 60 &&
      Math.abs(lastPoint.y - startPoint.y) <= 60
    );
  };

  const handleMouseClick = (
    event: React.MouseEvent<SVGSVGElement, MouseEvent>
  ) => {
    if (!isDrawing || videoRef.current?.paused || draggedPointIndex !== null) {
      return;
    }

    const { offsetX, offsetY } = event.nativeEvent;

    const clientWidth = videoRef.current?.clientWidth || 1;
    const clientHeight = videoRef.current?.clientHeight || 1;

    const scaleWidth = REF_WIDTH / clientWidth;
    const scaleHeight = REF_HEIGHT / clientHeight;

    const newPoint = {
      x: offsetX * scaleWidth,
      y: offsetY * scaleHeight,
    };

    if (currentPolygon.length >= 3) {
      const lastPoint = currentPolygon[currentPolygon.length - 1];
      const startPoint = currentPolygon[0];

      const isCloseToStart =
        Math.abs(newPoint.x - startPoint.x) <= 60 &&
        Math.abs(newPoint.y - startPoint.y) <= 60;
      const isCloseToLast =
        Math.abs(newPoint.x - lastPoint.x) <= 60 &&
        Math.abs(newPoint.y - lastPoint.y) <= 60;

      if (isPolygonClosed()) {
        return;
      }

      if (isCloseToStart || isCloseToLast) {
        join();
        return;
      }
    }

    setCurrentPolygon((prevPoints) => [...prevPoints, newPoint]);
  };

  const handleMouseDown = (
    event: React.MouseEvent<SVGCircleElement, MouseEvent>,
    polygonIndex: number,
    pointIndex: number
  ) => {
    if (!isDrawing) {
      return;
    }
    setDraggedPointIndex({ polygonIndex, pointIndex });
  };

  const handleMouseDrag = (
    event: React.MouseEvent<SVGSVGElement, MouseEvent>
  ) => {
    if (!isDrawing) {
      return;
    }
    if (draggedPointIndex !== null) {
      const { polygonIndex, pointIndex } = draggedPointIndex;
      const { offsetX, offsetY } = event.nativeEvent;

      const clientWidth = videoRef.current?.clientWidth || 1;
      const clientHeight = videoRef.current?.clientHeight || 1;

      const scaleWidth = REF_WIDTH / clientWidth;
      const scaleHeight = REF_HEIGHT / clientHeight;

      const updatedPoint = {
        x: offsetX * scaleWidth,
        y: offsetY * scaleHeight,
      };

      setPolygons((prevPolygons) => {
        const updatedPolygons = [...prevPolygons];
        const updatedPolygon = [...updatedPolygons[polygonIndex]];
        updatedPolygon[pointIndex] = updatedPoint;
        updatedPolygons[polygonIndex] = updatedPolygon;
        if (pointIndex !== 0 && pointIndex !== updatedPolygon.length - 1) {
          updatedPolygons[polygonIndex] = updatedPolygon;
          join(); // Trigger join logic only if it's not the first or last point
        }

        return updatedPolygons;
      });
    }
  };

  const handleMouseUp = () => {
    if (!isDrawing) {
      return;
    }
    setDraggedPointIndex(null);
  };

  const renderPolygons = () => {
    return polygons.map((polygon, polygonIndex) => (
      <g key={polygonIndex}>
        <polygon
          points={polygon.map((point) => `${point.x},${point.y}`).join(" ")}
          fill="transparent"
          stroke="red"
          strokeWidth="5"
        />
        {polygon.map((point, pointIndex) => (
          <circle
            key={pointIndex}
            cx={point.x}
            cy={point.y}
            r="15"
            fill="blue"
            onMouseDown={(event) =>
              handleMouseDown(event, polygonIndex, pointIndex)
            }
          />
        ))}
      </g>
    ));
  };

  const renderCurrentPolygon = () => {
    return (
      <g>
        {currentPolygon.map((point, pointIndex) => (
          <circle
            key={pointIndex}
            cx={point.x}
            cy={point.y}
            r="15"
            fill="red"
          />
        ))}
        <polyline
          points={currentPolygon
            .map((point) => `${point.x},${point.y}`)
            .join(" ")}
          fill="transparent"
          stroke="blue"
          strokeWidth="5"
        />
      </g>
    );
  };

  const join = () => {
    const lastPoint = currentPolygon[currentPolygon.length - 1];
    const startPoint = currentPolygon[0];

    setPolygons((prevPolygons) => [...prevPolygons, currentPolygon]);
    setCurrentPolygon([]);
  };
  const undo = () => {
    if (!isDrawing) {
      return;
    }
    if (currentPolygon.length > 0) {
      setCurrentPolygon((prevPoints) => prevPoints.slice(0, -1));
    } else if (polygons.length > 0) {
      setPolygons((prevPolygons) => prevPolygons.slice(0, -1));
    }
  };
  const clear = () => {
    if (!isDrawing) {
      return;
    }
    setPolygons([]);
    setCurrentPolygon([]);
  };
  useEffect(() => {
    // If drawing is stopped and there are polygons in currentPolygon, save them to polygons state
    if (!isDrawing && currentPolygon.length > 0) {
      setPolygons((prevPolygons) => [...prevPolygons, currentPolygon]);
      setCurrentPolygon([]);
    }
  }, [isDrawing]);
  return (
    <Box
      as="svg"
      ref={svgRef}
      position={"absolute"}
      width={"100%"}
      height={"100%"}
      zIndex={"1"}
      style={{
        border: "1px solid orange",
      }}
      viewBox={`0 0 ${REF_WIDTH} ${REF_HEIGHT}`}
      onClick={handleMouseClick}
      onMouseMove={handleMouseDrag}
      onMouseUp={handleMouseUp}
      onContextMenu={(e: { preventDefault: () => void }) => {
        undo();
      }}
      onDoubleClick={clear}>
      {renderPolygons()}
      {videoRef.current?.paused ? null : isDrawing && renderCurrentPolygon()}
    </Box>
  );
};

export default DrawZone;
