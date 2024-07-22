import { Box, Grid, GridItem } from "@chakra-ui/react";

function GridView() {
  return (
    <>
      <Grid
        templateColumns="repeat(2, 1fr)"
        templateRows="repeat(2, 1fr)"
        h={"100%"}
        maxH={"100%"}
        gap={6}>
        <GridItem w="100%" h="100%" bg="blue.500">
          <Box
            as="video"
            display={"block"}
            controls
            src="https://archive.org/download/BigBuckBunny_124/Content/big_buck_bunny_720p_surround.mp4"
            poster="https://peach.blender.org/wp-content/uploads/title_anouncement.jpg?x11217"
            objectFit="fill"
            sx={{
              aspectRatio: "16/9",
            }}
          />
        </GridItem>
        {/* <GridItem w="100%" h="100%" bg="blue.500">
          <Box
            as="video"
            display={"block"}
            controls
            src="https://archive.org/download/BigBuckBunny_124/Content/big_buck_bunny_720p_surround.mp4"
            poster="https://peach.blender.org/wp-content/uploads/title_anouncement.jpg?x11217"
            alt="Big Buck Bunny"
            objectFit="fill"
            sx={{
              aspectRatio: "16/9",
            }}
          />
        </GridItem> */}
        {/* <GridItem w="100%" h="100%" bg="blue.500">
          <Box
            as="video"
            display={"block"}
            controls
            src="https://archive.org/download/BigBuckBunny_124/Content/big_buck_bunny_720p_surround.mp4"
            poster="https://peach.blender.org/wp-content/uploads/title_anouncement.jpg?x11217"
            alt="Big Buck Bunny"
            objectFit="fill"
            sx={{
              aspectRatio: "16/9",
            }}
          />
        </GridItem> */}
        {/* <GridItem w="100%" h="100%" bg="blue.500">
          <Box
            as="video"
            display={"block"}
            controls
            src="https://archive.org/download/BigBuckBunny_124/Content/big_buck_bunny_720p_surround.mp4"
            poster="https://peach.blender.org/wp-content/uploads/title_anouncement.jpg?x11217"
            alt="Big Buck Bunny"
            objectFit="fill"
            sx={{
              aspectRatio: "16/9",
            }}
          />
        </GridItem> */}
      </Grid>
    </>
  );
}

export default GridView;
