import { Box, Flex, Heading, LinkBox, VStack } from "@chakra-ui/react";
import { Link as RouterLink } from "react-router-dom";

function Missing() {
  return (
    <Flex align="center" justify="center">
      <VStack>
        <Heading size="md">Page not found!</Heading>
        <Box>Well that is disappointing</Box>
        <LinkBox as={RouterLink} to={""}>
          Go to Home
        </LinkBox>
      </VStack>
    </Flex>
  );
}

export default Missing;
