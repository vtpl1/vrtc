import { Grid, GridItem } from "@chakra-ui/react";
import { useEffect, useMemo, useState } from "react";
import VideoPlayer from "./VideoPlayer";

export type GridDetails = {
  grid_style_id: number;
  id: number;
  width: string;
  height: string;
  total_cols: number;
  total_rows: number;
};
export const GridSizes = [1, 2, 4, 6, 9, 12, 16, 25, 36, 49];

function gridMaker(col: number = 4): GridDetails {
  col = GridSizes.filter((v) => v >= col)[0] || 4;
  const grid_details: GridDetails = {
    grid_style_id: 1,
    id: 0,
    width: "100%",
    height: "100%",
    total_cols: 1,
    total_rows: 1,
  };
  console.log("col: ", col);
  switch (col) {
    case 1:
      grid_details.grid_style_id = 1;
      grid_details.total_cols = 1;
      grid_details.total_rows = 1;
      grid_details.width = "100%";
      grid_details.height = "100%";
      break;
    case 2:
      grid_details.grid_style_id = 2;
      grid_details.total_cols = 2;
      grid_details.total_rows = 1;
      grid_details.width = "50%";
      grid_details.height = "100%";
      break;
    case 4:
      grid_details.grid_style_id = 4;
      grid_details.total_cols = 2;
      grid_details.total_rows = 2;
      grid_details.width = "50%";
      grid_details.height = "50%";
      break;
    case 6:
      grid_details.grid_style_id = 6;
      grid_details.total_cols = 3;
      grid_details.total_rows = 2;
      grid_details.width = "33.33%";
      grid_details.height = "50%";
      break;
    case 9:
      grid_details.grid_style_id = 9;
      grid_details.total_cols = 3;
      grid_details.total_rows = 3;
      grid_details.width = "33.33%";
      grid_details.height = "33.33%";
      break;
    case 12:
      grid_details.grid_style_id = 12;
      grid_details.total_cols = 4;
      grid_details.total_rows = 3;
      grid_details.width = "25%";
      grid_details.height = "33.33%";
      break;
    case 16:
      grid_details.grid_style_id = 16;
      grid_details.total_cols = 4;
      grid_details.total_rows = 4;
      grid_details.width = "25%";
      grid_details.height = "25%";
      break;
    case 25:
      grid_details.grid_style_id = 25;
      grid_details.total_cols = 5;
      grid_details.total_rows = 5;
      grid_details.width = "20%";
      grid_details.height = "20%";
      break;
    case 36:
      grid_details.grid_style_id = 36;
      grid_details.total_cols = 6;
      grid_details.total_rows = 6;
      grid_details.width = "16.66%";
      grid_details.height = "16.66%";
      break;
    case 49:
      grid_details.grid_style_id = 49;
      grid_details.total_cols = 7;
      grid_details.total_rows = 7;
      grid_details.width = "14.285%";
      grid_details.height = "14.285%";
      break;
    default:
      break;
  }
  return grid_details;
}

function getGrids(col: number): GridDetails[] {
  const grid_details: GridDetails = gridMaker(col);
  const grids: GridDetails[] = Array.from(
    { length: grid_details.grid_style_id },
    (_, i) => {
      return {
        ...grid_details,
        id: i,
      };
    }
  );
  return grids;
}

function GridSelector({ numberOfGrids }: { numberOfGrids: number }) {
  console.log("numberOfGrids: ", numberOfGrids);
  const [rows, setRows] = useState(1);
  const [cols, setCols] = useState(1);

  const grids = useMemo(() => {
    return getGrids(numberOfGrids);
  }, [numberOfGrids]);
  useEffect(() => {
    if (grids && grids.length > 0) {
      setRows(grids[0].total_rows);
      setCols(grids[0].total_cols);
    }
  }, [grids]);

  return (
    <Grid
      templateRows={"repeat(" + rows + ", 1fr)"}
      templateColumns={"repeat(" + cols + ", 1fr)"}
      h={"100%"}>
      {grids.map((item) => {
        return (
          <GridItem key={item.id} w={"100%"} h={"100%"}>
            <VideoPlayer />
          </GridItem>
        );
      })}
    </Grid>
  );
}

export default GridSelector;
