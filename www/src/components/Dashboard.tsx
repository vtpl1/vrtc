import { Box, HStack, Tag, RadioGroup, Radio } from "@chakra-ui/react";
import GridSelector from "./GridSelector";
import { useState } from "react";

function Dashboard() {
  const [channels, setChannels] = useState<number>(4);
  return (
    <Box>
      <HStack>
        <HStack>
          <Tag>Select Number of Grids</Tag>
          <RadioGroup
            value={String(channels)}
            onChange={(value) => setChannels(parseInt(value))}>
            <HStack spacing={4}>
              <Radio value="1">1</Radio>
              <Radio value="4">4</Radio>
              <Radio value="9">9</Radio>
              {/* <Radio value="12">12</Radio> */}
              <Radio value="16">16</Radio>
            </HStack>
          </RadioGroup>
        </HStack>
      </HStack>
      <GridSelector numberOfGrids={channels} />
    </Box>
  );
}

export default Dashboard;
