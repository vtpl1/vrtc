// https://github.com/dimitrisraptis96/chakra-ui-navbar
import { CloseIcon, HamburgerIcon, MoonIcon, SunIcon } from "@chakra-ui/icons";
import {
  Avatar,
  Box,
  Button,
  Center,
  Flex,
  HStack,
  IconButton,
  Image,
  Link,
  Menu,
  MenuButton,
  MenuDivider,
  MenuItem,
  MenuList,
  Stack,
  VStack,
  useColorMode,
  useColorModeValue,
  useDisclosure,
  useToast,
} from "@chakra-ui/react";
import { lorelei } from "@dicebear/collection";
import { createAvatar } from "@dicebear/core";
import { Select } from "chakra-react-select";
import { ReactNode, useMemo } from "react";
import { Link as RouterLink, useNavigate } from "react-router-dom";

import { useTranslation } from "react-i18next";
import { useAppDispatch } from "../app/hooks";
import { LanguageOptions, useLanguage } from "../context/LanguageContext";
// import { signOut, useAuth } from "../features/auth/auth-slice";
import Links from "../links.ts";

const NavLink = ({ children, to }: { children: ReactNode; to: string }) => (
  <Link
    as={RouterLink}
    to={to}
    px={2}
    py={1}
    rounded={"md"}
    _hover={{
      textDecoration: "none",
      bg: useColorModeValue("gray.200", "gray.700"),
    }}>
    {children}
  </Link>
);

function Logo() {
  return (
    <Box>
      <Image boxSize="60px" src="/videonetics-logo.svg" alt="Logo" />
    </Box>
  );
}

function RightMenu() {
  const { colorMode, toggleColorMode } = useColorMode();
  // const auth = useAuth();
  const dispatch = useAppDispatch();
  const navigate = useNavigate();
  const toast = useToast();
  const { language, handleLocaleChange } = useLanguage();
  const avatar = useMemo(() => {
    return createAvatar(lorelei, {
      size: 128,
      // seed: auth?.user?.fullName,
      seed: "Admin",
      // ... other options
    }).toDataUri();
  }, []);
  return (
    <Flex alignItems={"center"}>
      <Stack direction={"row"} spacing={2}>
        {/* <DownloadPage /> */}
        <Button onClick={toggleColorMode}>
          {colorMode === "light" ? <MoonIcon /> : <SunIcon />}
        </Button>
        <Select
          onChange={(newValue, actionMeta) => {
            if (newValue) {
              console.log(newValue.value);
              handleLocaleChange(newValue.value);
            }
          }}
          value={{ label: language.locale, value: language.locale }}
          options={LanguageOptions}
        />
        <Menu>
          <MenuButton
            as={Button}
            rounded={"full"}
            variant={"link"}
            cursor={"pointer"}
            minW={0}>
            <Avatar size={"sm"} src={avatar} />
          </MenuButton>

          <MenuList alignItems={"center"}>
            <br />
            <Center>
              <Avatar size={"2xl"} src={avatar} />
            </Center>
            <br />
            <Center>
              <p>{/* {auth?.user?.fullName} */}</p>
            </Center>
            <br />
            <MenuDivider />
            <MenuItem
              onClick={async () => {
                // try {
                //   await signOut();
                //   navigate("/");
                // } catch (err) {
                //   toast({
                //     status: "error",
                //     title: "Error",
                //     description: "Oh no, there was an error!",
                //     isClosable: true,
                //   });
                // }
              }}>
              Logout
            </MenuItem>
          </MenuList>
        </Menu>
      </Stack>
    </Flex>
  );
}

function MenuContent() {
  const { t } = useTranslation();
  const { language } = useLanguage();
  return Links.map((link) => (
    <NavLink key={link} to={language.locale + "/" + link.toLowerCase()}>
      {t(link.toLowerCase())}
    </NavLink>
  ));
}

export default function NavBar() {
  const { isOpen, onOpen, onClose } = useDisclosure();
  return (
    <Box bg={useColorModeValue("gray.100", "gray.900")} px={4}>
      <Flex h={16} alignItems={"center"} justifyContent={"space-between"}>
        <IconButton
          size={"md"}
          icon={isOpen ? <CloseIcon /> : <HamburgerIcon />}
          aria-label={"Open Menu"}
          display={{ md: "none" }}
          onClick={isOpen ? onClose : onOpen}
        />

        <HStack spacing={8} alignItems={"center"}>
          <Logo />
          <HStack as={"nav"} spacing={4} display={{ base: "none", md: "flex" }}>
            <MenuContent />
          </HStack>
        </HStack>
        <RightMenu></RightMenu>
      </Flex>
      {isOpen ? (
        <Box pb={4} display={{ md: "none" }}>
          <VStack as={"nav"} spacing={4}>
            <MenuContent />
          </VStack>
        </Box>
      ) : null}
    </Box>
  );
}
