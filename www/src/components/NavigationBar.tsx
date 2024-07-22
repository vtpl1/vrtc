import { Button, HStack, IconButton } from "@chakra-ui/react";
import {
  ArrowBackIcon,
  ArrowForwardIcon,
  SearchIcon,
  ViewIcon,
} from "@chakra-ui/icons";
import React from "react";

const NavigationBar = ({
  siteId,
  channelId,
}: {
  siteId: number;
  channelId: number;
}) => {
  return (
    <HStack>
      <IconButton aria-label="back" icon={<ArrowBackIcon />} />
      <IconButton aria-label="forward" icon={<ArrowForwardIcon />} />
      <IconButton aria-label="view" icon={<ViewIcon />} />
    </HStack>
  );
};

export default NavigationBar;
