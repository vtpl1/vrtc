import {
  Box,
  Card,
  CardBody,
  CardHeader,
  HStack,
  Input,
  InputGroup,
  InputLeftAddon,
  Image,
  Divider,
  Alert,
  AlertIcon,
  Grid,
  Tag,
  Popover,
  PopoverTrigger,
  PopoverContent,
  Stack,
  CardFooter,
  Button,
  Text,
  RadioGroup,
  Radio,
  Spinner,
  Select,
  SimpleGrid,
  useDisclosure,
  Drawer,
  DrawerBody,
  DrawerContent,
  DrawerHeader,
  DrawerOverlay,
  DrawerCloseButton,
  IconButton,
  Tooltip,
} from "@chakra-ui/react";
import { ImageGallery } from "react-image-grid-gallery";
import * as ExcelJS from "exceljs";

import { useDebounce } from "@uidotdev/usehooks";
import React, { useEffect, useState } from "react";
import {
  AttributeEvent,
  BottomColor,
  BottomType,
  GetAttributeEventApiResponse,
  HasBag,
  HasHat,
  ObjectType,
  SexType,
  TopColor,
  TopType,
  VehicleColor,
  VehicleType,
  useGetAttributeEventQuery,
  useUpdateAttributeEventMutation,
} from "../../services/graphApiGen";
import {
  AddIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  DownloadIcon,
  SmallCloseIcon,
} from "@chakra-ui/icons";
import { saveAs } from "file-saver";
import * as XLSX from "xlsx";
import axios from "axios";
import { FaEdit, FaFilter, FaRegSave } from "react-icons/fa";
type booleanInterface = {
  yes: true;
  no: false;
};

export default function Events() {
  const [siteId, setSiteId] = useState<number>(0);
  const debouncedSiteId = useDebounce(siteId, 500);
  const [channelId, setChannelId] = useState<number>(0);
  const debouncedChannelId = useDebounce(channelId, 500);
  const [startTime, setStartTime] = useState<number>(Date.now() - 3600000);
  const [endTime, setEndTime] = useState<number>(Date.now());
  const [showAlert, setShowAlert] = useState<boolean>(false);
  const [sortedData, setSortedData] = useState<GetAttributeEventApiResponse>();
  const [currentPage, setCurrentPage] = useState<number>(0);
  const pageSize = 48;
  const [sex, setSex] = useState<string>("all");
  const [edited, setEdited] = useState<string>("all");
  const [hasBag, setHasBag] = useState<string>("all");
  const [hasHat, setHasHat] = useState<string>("all");
  const [vehicleColor, setVehicleColor] = useState<string>("all");
  const [vehicleType, setVehicleType] = useState<string>("all");
  const [topType, setTopType] = useState<string>("all");
  const [topColor, setTopColor] = useState<string>("all");
  const [bottomType, setBottomType] = useState<string>("all");
  const [bottomColor, setBottomColor] = useState<string>("all");
  const [objectType, setObjectType] = useState<number>(0);

  const { data, isError, isLoading, isFetching, refetch } =
    useGetAttributeEventQuery({
      siteId: debouncedSiteId,
      channelId: debouncedChannelId,
      startTime: startTime,
      endTime: endTime,
      sex: sex,
      hasBag: hasBag,
      hasHat: hasHat,
      edited: edited,
      objectType: objectType,
      topType: topType,
      topColor: topColor,
      bottomType: bottomType,
      bottomColor: bottomColor,
      vehicleColor: vehicleColor,
      vehicleType: vehicleType,
      skip: currentPage * pageSize,
      limit: pageSize,
    });

  useEffect(() => {
    if (edited === "edited") setHasBag("all");
    setHasHat("all");
    setVehicleColor("all");
    setVehicleType("all");
    setTopType("all");
    setTopColor("all");
    setBottomType("all");
    setBottomColor("all");
    setSex("all");
    setObjectType(0);
  }, [edited]);

  useEffect(() => {
    if (data) setSortedData(data);
    // console.log("Data", data);
    console.log("data", data);
  }, [data]);
  useEffect(() => {
    console.log("sortedData", sortedData);
  }, [sortedData]);

  useEffect(() => {
    if (sortedData && sortedData?.length < 1) {
      setShowAlert(true);
    } else {
      setShowAlert(false);
    }
  }, [sortedData]);
  const handlePrevPage = () => {
    setCurrentPage((prev) => Math.max(prev - 1, 0));
    refetch();
  };

  const handleNextPage = () => {
    setCurrentPage((prev) => prev + 1);
  };
  const toDataURL = async (url: string | undefined) => {
    try {
      console.log("Fetching image......", url);
      if (url) {
        const response = await axios.get(url, {
          responseType: "arraybuffer",
        });
        const arrayBuffer = response.data;
        const base64String = btoa(
          new Uint8Array(arrayBuffer).reduce(
            (data, byte) => data + String.fromCharCode(byte),
            ""
          )
        );
        return base64String;
      }
    } catch (error) {
      console.error("Error fetching image:", error);
    }
  };

  const handleExportExcel = async () => {
    const workBook = new ExcelJS.Workbook();
    const sheet = workBook.addWorksheet("Sheet1");
    sheet.properties.defaultRowHeight = 80;

    sheet.columns = [
      {
        header: "Event Time",
        key: "eventTime",
      },
      {
        header: "Top Type",
        key: "topType",
      },
      {
        header: "Top Color",
        key: "topColor",
      },
      {
        header: "Bottom Type",
        key: "bottomType",
      },
      {
        header: "Bottom Color",
        key: "bottomColor",
      },
      {
        header: "Vehicle Type",
        key: "vehicleType",
      },
      {
        header: "Vehicle Color",
        key: "vehicleColor",
      },
      {
        header: "Has Bag",
        key: "hasBag",
      },
      {
        header: "Has Hat",
        key: "hasHat",
      },
      {
        header: "Object Type",
        key: "objectType",
      },
      {
        header: "Sex",
        key: "sex",
      },
      {
        header: "Snap",
        key: "snap",
      },
    ];
    const promise = Promise.all(
      sortedData!.map(async (obj, index) => {
        const rowNumber = index + 1;
        sheet.addRow({
          eventTime:(new Date(obj.startTimeStamp)).toLocaleString(),
          topType: obj.topType,
          topColor: obj.topColor,
          bottomType: obj.bottomType,
          bottomColor: obj.bottomColor,
          vehicleType: obj.vehicleType,
          vehicleColor: obj.vehicleColor,
          hasBag: obj.hasBag,
          hasHat: obj.hasHat,
          objectType: obj.objectType,
          sex: obj.sex,
        });

        console.log(obj.snap);
        const result = await toDataURL(obj.snap);

        const splitted = obj.snap?.split(".");
        let extName: "jpeg" | "png" | "gif"= "jpeg";

        if (splitted && splitted.length > 0) {
          extName = splitted[splitted.length - 1] as "jpeg" | "png" | "gif";
        }

        if (result) {
          const base64Image = result.split(";base64,").pop();
          if (base64Image) {
            const imageId2 = workBook.addImage({
              base64: base64Image,
              extension: "png",
            });

            sheet.addImage(imageId2, {
              editAs: "oneCell",
              tl: { col: 11, row: rowNumber },
              ext: { width: 50, height: 105 },

            });
            return;
          }
        }
      })
    );

    promise.then(() => {
      workBook.xlsx.writeBuffer().then(function (sortedData) {
        const blob = new Blob([sortedData], {
          type: "",
        });
        const url = window.URL.createObjectURL(blob);
        const anchor = document.createElement("a");
        anchor.href = url;
        anchor.download = "events.xlsx";
        anchor.click();
        window.URL.revokeObjectURL(url);
      });
    });
  };

  useEffect(() => {
    console.log("SiteId:", siteId);
    console.log("ChannelId:", channelId);
    console.log("startTime:", startTime);
    console.log("endTime:", endTime);
  }, [debouncedSiteId, debouncedChannelId, startTime, endTime]);
  const toLocalISOString = (epochTime: number) => {
    const date = new Date(epochTime);
    const offset = date.getTimezoneOffset() * 60000;
    const localISOTime = new Date(date.getTime() - offset)
      .toISOString()
      .slice(0, 16);
    return localISOTime;
  };
  const [editSdp] = useUpdateAttributeEventMutation();

  const [newSexValue, setNewSexValue] = useState<string>("");
  const [editSexIndex, setEditSexIndex] = useState<string>("");
  const handleUpdateSex = async (objId: string, value: string) => {
    try {
      await editSdp({
        key: objId,
        value: { "metaAttributeEvent.sex": value },
      });

      setSortedData((prevData) =>
        prevData?.map((item) =>
          item.objectId === objId ? { ...item, sex: value as SexType } : item
        )
      );
    } catch (error) {
      console.log("Error during mutation:", error);
    }
  };
  const [editTopColorIndex, setEditTopColorIndex] = useState<string>("");
  const [newTopColorValue, setNewTopColorValue] = useState<string>("");
  const handleUpdateTopColor = async (objId: string, value: string) => {
    try {
      await editSdp({
        key: objId,
        value: { "metaAttributeEvent.topColor": value },
      });

      setSortedData((prevData) =>
        prevData?.map((item) =>
          item.objectId === objId
            ? { ...item, topColor: value as TopColor }
            : item
        )
      );
    } catch (error) {
      console.log("Error during mutation:", error);
    }
  };
  const [editTopTypeIndex, setEditTopTypeIndex] = useState<string>("");
  const [newTopTypeValue, setNewTopTypeValue] = useState<string>("");
  const handleUpdateTopType = async (objId: string, value: string) => {
    try {
      await editSdp({
        key: objId,
        value: { "metaAttributeEvent.topType": value },
      });

      setSortedData((prevData) =>
        prevData?.map((item) =>
          item.objectId === objId
            ? { ...item, topType: value as TopType }
            : item
        )
      );
    } catch (error) {
      console.log("Error during mutation:", error);
    }
  };
  const [editVehicleTypeIndex, setEditVehicleTypeIndex] = useState<string>("");
  const [newVehicleTypeValue, setNewVehicleTypeValue] = useState<string>("");
  const handleUpdateVehicleType = async (objId: string, value: string) => {
    try {
      await editSdp({
        key: objId,
        value: { "metaAttributeEvent.vehicleType": value },
      });

      setSortedData((prevData) =>
        prevData?.map((item) =>
          item.objectId === objId
            ? { ...item, vehicleType: value as VehicleType }
            : item
        )
      );
    } catch (error) {
      console.log("Error during mutation:", error);
    }
  };
  const [editVehicleColorIndex, setEditVehicleColorIndex] =
    useState<string>("");
  const [newVehicleColorValue, setNewVehicleColorValue] = useState<string>("");
  const handleUpdateVehicleColor = async (objId: string, value: string) => {
    try {
      await editSdp({
        key: objId,
        value: { "metaAttributeEvent.vehicleColor": value },
      });

      setSortedData((prevData) =>
        prevData?.map((item) =>
          item.objectId === objId
            ? { ...item, vehicleColor: value as VehicleColor }
            : item
        )
      );
    } catch (error) {
      console.log("Error during mutation:", error);
    }
  };
  const [editBottomColorIndex, setEditBottomColorIndex] = useState<string>("");
  const [newBottomColorValue, setNewBottomColorValue] = useState<string>("");
  const handleUpdateBottomColor = async (objId: string, value: string) => {
    try {
      await editSdp({
        key: objId,
        value: { "metaAttributeEvent.bottomColor": value },
      });

      setSortedData((prevData) =>
        prevData?.map((item) =>
          item.objectId === objId
            ? { ...item, bottomColor: value as BottomColor }
            : item
        )
      );
    } catch (error) {
      console.log("Error during mutation:", error);
    }
  };
  const [editBottomTypeIndex, setEditBottomTypeIndex] = useState<string>("");
  const [newBottomTypeValue, setNewBottomTypeValue] = useState<string>("");
  const handleUpdateBottomType = async (objId: string, value: string) => {
    try {
      await editSdp({
        key: objId,
        value: { "metaAttributeEvent.bottomType": value },
      });

      setSortedData((prevData) =>
        prevData?.map((item) =>
          item.objectId === objId
            ? { ...item, bottomType: value as BottomType }
            : item
        )
      );
    } catch (error) {
      console.log("Error during mutation:", error);
    }
  };
  const [editHasBagIndex, setEditHasBagIndex] = useState<string>("");
  const [newHasBagValue, setNewHasBagValue] = useState<string>("");
  const handleUpdateHasBag = async (objId: string, value: string) => {
    try {
      await editSdp({
        key: objId,
        value: { "metaAttributeEvent.presenceOfBag": value },
      });

      setSortedData((prevData) =>
        prevData?.map((item) =>
          item.objectId === objId ? { ...item, hasBag: value as HasBag } : item
        )
      );
    } catch (error) {
      console.log("Error during mutation:", error);
    }
  };
  const [editHasHatIndex, setEditHasHatIndex] = useState<string>("");
  const [newHasHatValue, setNewHasHatValue] = useState<string>("");
  const handleUpdateHasHat = async (objId: string, value: string) => {
    try {
      await editSdp({
        key: objId,
        value: { "metaAttributeEvent.presenceOfHeadeDress": value },
      });

      setSortedData((prevData) =>
        prevData?.map((item) =>
          item.objectId === objId ? { ...item, hasHat: value as HasHat } : item
        )
      );
    } catch (error) {
      console.log("Error during mutation:", error);
    }
  };
  const [editObjectTypeIndex, setEditObjectTypeIndex] = useState<string>("");
  const [newObjectTypeValue, setNewObjectTypeValue] = useState<number>(0);
  const handleUpdateObjectType = async (objId: string, value: number) => {
    try {
      await editSdp({
        key: objId,
        value: { "metaAttributeEvent.objectType": value },
      });

      setSortedData((prevData) =>
        prevData?.map((item) =>
          item.objectId === objId
            ? { ...item, objectType: value as ObjectType }
            : item
        )
      );
    } catch (error) {
      console.log("Error during mutation:", error);
    }
  };
  const { isOpen, onOpen, onClose } = useDisclosure();
  return (
    <Box>
      <Card>
        <CardHeader>
          <HStack>
            <Tooltip label="Filter Events">
              <IconButton
                onClick={onOpen}
                variant="outline"
                fontSize="20px"
                icon={<FaFilter />}
                aria-label={"Filter Events"}
              />
            </Tooltip>
            <Drawer isOpen={isOpen} placement="left" onClose={onClose}>
              <DrawerOverlay />
              <DrawerContent>
                <DrawerCloseButton />
                <DrawerHeader borderBottomWidth="1px" padding={2}>
                  Filter Options {"   "}
                  <Button
                    size={"xs"}
                    onClick={() => {
                      setHasBag("all");
                      setHasHat("all");
                      setVehicleColor("all");
                      setVehicleType("all");
                      setTopType("all");
                      setTopColor("all");
                      setSex("all");
                      setBottomType("all");
                      setBottomColor("all");
                      setObjectType(0);
                      setEdited("all");
                    }}>
                    Reset
                  </Button>
                </DrawerHeader>
                <DrawerBody>
                  <Stack spacing={15}>
                    <Card>
                      <InputGroup width="auto">
                        <InputLeftAddon>Object Type</InputLeftAddon>
                        <Select
                          value={objectType}
                          onChange={(e) =>
                            setObjectType(Number(e.target.value))
                          }>
                          <option value="1">Human</option>
                          <option value="2">Vehicle</option>
                          <option value="0">All</option>
                        </Select>
                      </InputGroup>
                    </Card>
                    <Divider />
                    {objectType !== 2 && (
                      <>
                        <Card>
                          <InputGroup width="auto">
                            <InputLeftAddon>Top Type</InputLeftAddon>
                            <Select
                              value={topType}
                              onChange={(e) => setTopType(e.target.value)}>
                              <option value="jacket">Jacket</option>
                              <option value="shirt">Shirt</option>
                              <option value="tshirt">Tshirt</option>
                              <option value="un">Unknown</option>
                              <option value="all">All</option>
                            </Select>
                          </InputGroup>
                        </Card>
                        <Divider />
                      </>
                    )}
                    {objectType !== 2 && (
                      <>
                        <Card>
                          <InputGroup width="auto">
                            <InputLeftAddon>Top Color</InputLeftAddon>
                            <Select
                              value={topColor}
                              onChange={(e) => setTopColor(e.target.value)}>
                              <option value="black">Black</option>
                              <option value="blue">Blue</option>
                              <option value="green">Green</option>
                              <option value="red">Red</option>
                              <option value="white">White</option>
                              <option value="yellow">Yellow</option>
                              <option value="un">Unknown</option>
                              <option value="all">All</option>
                            </Select>
                          </InputGroup>
                        </Card>
                        <Divider />
                      </>
                    )}
                    {objectType !== 2 && (
                      <>
                        <Card>
                          <InputGroup width="auto">
                            <InputLeftAddon>Bottom Type</InputLeftAddon>
                            <Select
                              value={bottomType}
                              onChange={(e) => setBottomType(e.target.value)}>
                              <option value="longpants">Longpants</option>
                              <option value="shorts">Shorts</option>
                              <option value="skirt">Skirt</option>
                              <option value="unknown">Unknown</option>
                              <option value="all">All</option>
                            </Select>
                          </InputGroup>
                        </Card>
                        <Divider />{" "}
                      </>
                    )}
                    {objectType !== 2 && (
                      <>
                        {" "}
                        <Card>
                          <InputGroup width="auto">
                            <InputLeftAddon>Bottom Color</InputLeftAddon>
                            <Select
                              value={bottomColor}
                              onChange={(e) => setBottomColor(e.target.value)}>
                              <option value="black">Black</option>
                              <option value="blue">Blue</option>
                              <option value="green">Green</option>
                              <option value="red">Red</option>
                              <option value="white">White</option>
                              <option value="yellow">Yellow</option>
                              <option value="unknown">Unknown</option>
                              <option value="all">All</option>
                            </Select>
                          </InputGroup>
                        </Card>
                        <Divider />
                      </>
                    )}
                    {objectType !== 2 && (
                      <>
                        {" "}
                        <Card>
                          <InputGroup width="auto">
                            <InputLeftAddon>Sex</InputLeftAddon>
                            <Select
                              value={sex}
                              onChange={(e) => setSex(e.target.value)}>
                              <option value="male">Male</option>
                              <option value="Female">Female</option>
                              <option value="all">All</option>
                            </Select>
                          </InputGroup>
                        </Card>
                        <Divider />
                      </>
                    )}
                    {objectType !== 2 && (
                      <>
                        <Card>
                          <InputGroup width="auto">
                            <InputLeftAddon>Has Hat</InputLeftAddon>
                            <Select
                              value={hasHat}
                              onChange={(e) => setHasHat(e.target.value)}>
                              <option value="yes">Yes</option>
                              <option value="no">No</option>
                              <option value="all">All</option>
                            </Select>
                          </InputGroup>
                        </Card>
                        <Divider />{" "}
                      </>
                    )}
                    {objectType !== 2 && (
                      <>
                        <Card>
                          <InputGroup width="auto">
                            <InputLeftAddon>Has Bag</InputLeftAddon>
                            <Select
                              value={hasBag}
                              onChange={(e) => setHasBag(e.target.value)}>
                              <option value="yes">Yes</option>
                              <option value="no">No</option>
                              <option value="all">All</option>
                            </Select>
                          </InputGroup>
                        </Card>
                        <Divider />
                      </>
                    )}
                    {objectType !== 1 && (
                      <>
                        <Card>
                          <InputGroup width="auto">
                            <InputLeftAddon>Vehicle Color</InputLeftAddon>
                            <Select
                              value={vehicleColor}
                              onChange={(e) => setVehicleColor(e.target.value)}>
                              <option value="black">Black</option>
                              <option value="blue">Blue</option>
                              <option value="brown">Brown</option>
                              <option value="gray">Gray</option>
                              <option value="green">Green</option>
                              <option value="orange">Orange</option>
                              <option value="pink">Pink</option>
                              <option value="purple">Purple</option>
                              <option value="red">Red</option>
                              <option value="silver">Silver</option>
                              <option value="white">White</option>
                              <option value="yellow">Yellow</option>
                              <option value="un">Unknown</option>
                              <option value="others">Others</option>
                              <option value="all">All</option>
                            </Select>
                          </InputGroup>
                        </Card>
                        <Divider />
                      </>
                    )}
                    {objectType !== 1 && (
                      <>
                        <Card>
                          <InputGroup width="auto">
                            <InputLeftAddon>Vehicle Type</InputLeftAddon>
                            <Select
                              value={vehicleType}
                              onChange={(e) => setVehicleType(e.target.value)}>
                              <option value="auto">Auto</option>
                              <option value="bicycle">Bicycle</option>
                              <option value="bus">Bus</option>
                              <option value="car">Car</option>
                              <option value="carrier">Carrier</option>
                              <option value="motorbike">Motorbike</option>
                              <option value="truck">Truck</option>
                              <option value="van">Van</option>
                              <option value="un">Unknown</option>
                              <option value="others">Others</option>
                              <option value="all">All</option>
                            </Select>
                          </InputGroup>
                        </Card>
                        <Divider />
                      </>
                    )}
                    <Card>
                      <InputGroup width="auto">
                        <InputLeftAddon>Edited</InputLeftAddon>
                        <Select
                          value={edited}
                          onChange={(e) => setEdited(e.target.value)}>
                          <option value="yes">Yes</option>
                          <option value="all">All</option>
                        </Select>
                      </InputGroup>
                    </Card>
                    <Divider />
                  </Stack>
                </DrawerBody>
              </DrawerContent>
            </Drawer>

            <InputGroup>
              <InputLeftAddon>Start Time</InputLeftAddon>
              <Input
                type="datetime-local"
                placeholder="Start Time"
                value={toLocalISOString(startTime)}
                onChange={(e) => {
                  setStartTime(new Date(e.target.value).getTime());
                }}
                max={toLocalISOString(Date.now())}
              />
            </InputGroup>
            <InputGroup>
              <InputLeftAddon>End Time</InputLeftAddon>
              <Input
                type="datetime-local"
                placeholder="Start Time"
                value={toLocalISOString(endTime)}
                onChange={(e) => {
                  setEndTime(new Date(e.target.value).getTime());
                }}
                max={toLocalISOString(Date.now())}
              />
            </InputGroup>
            <Button onClick={handleExportExcel}>
              <DownloadIcon />
            </Button>
          </HStack>
        </CardHeader>
        <Divider />
        <CardBody>
          {isError ? (
            <Alert status="warning">
              <AlertIcon />
              SERVER_ERROR
            </Alert>
          ) : isLoading ? (
            <Alert status="warning">
              <AlertIcon />
              LOADING.. <Spinner />
            </Alert>
          ) : isFetching ? (
            <Alert status="warning">
              <AlertIcon />
              FETCHING_DATA
            </Alert>
          ) : showAlert ? (
            <Alert status="warning">
              <AlertIcon />
              NO_DATA_AVILABLE
            </Alert>
          ) : (
            <Grid
              templateColumns="repeat(auto-fill, minmax(145px, 1fr))"
              gap={1}>
              {sortedData?.map((obj, index) => (
                <React.Fragment key={index}>
                  <Popover
                    trigger="hover"
                    closeOnBlur
                    placement="left"
                    size={"100px"}>
                    <PopoverTrigger>
                      {obj.snap ? (
                        <Tag
                          height={"300px"}
                          width={"150px"}
                          justifyContent="center">
                          {" "}
                          <Image
                            maxH={"249px"}
                            maxW={"149px"}
                            // boxSize="inherit"
                            _focus={{ outline: "none", shadow: "outline" }}
                            loading="lazy"
                            borderRadius="md"
                            src={obj.snap}
                            fallbackSrc="https://via.placeholder.com/100"
                          />
                        </Tag>
                      ) : (
                        <Box boxSize="50px"></Box>
                      )}
                    </PopoverTrigger>
                    <PopoverContent>
                      <Box>
                        <Card>
                          <CardHeader>
                            <Stack>
                              <Tag>
                                Event Time :{" "}
                                {new Date(obj.startTimeStamp).toLocaleString()}
                              </Tag>
                              <HStack>
                                <Tag>SiteId : {obj.siteId}</Tag>
                                <Tag>ChannelId : {obj.channelId}</Tag>
                              </HStack>
                              <Tag>TrackId : {obj.trackId}</Tag>
                              {editTopColorIndex === obj.objectId ? (
                                <Tag>
                                  <InputGroup size="xs" width="auto">
                                    <InputLeftAddon>Top Color</InputLeftAddon>
                                    <Select
                                      // value={obj.topColor}
                                      placeholder="Select Top color"
                                      onChange={(e) =>
                                        setNewTopColorValue(e.target.value)
                                      }>
                                      <option value="black">black</option>
                                      <option value="blue">blue</option>
                                      <option value="green">green</option>
                                      <option value="red">un</option>
                                      <option value="white">white</option>
                                      <option value="yellow">yellow</option>
                                      <option value="un">un</option>
                                    </Select>
                                  </InputGroup>
                                  <Button
                                    onClick={() => {
                                      setEditTopColorIndex("");
                                      handleUpdateTopColor(
                                        obj.objectId,
                                        newTopColorValue
                                      );
                                    }}
                                    size={"xs"}>
                                    {" "}
                                    <FaRegSave />
                                  </Button>
                                  <Button
                                    size={"xs"}
                                    onClick={() => {
                                      setEditTopColorIndex("");
                                    }}>
                                    <SmallCloseIcon />
                                  </Button>
                                </Tag>
                              ) : (
                                obj.objectType === 1 && (
                                  <Tag>
                                    Top Color : {obj.topColor}{" "}
                                    <Button
                                      size={"xs"}
                                      onClick={() => {
                                        setEditTopColorIndex(obj.objectId);
                                      }}>
                                      <FaEdit />
                                    </Button>
                                  </Tag>
                                )
                              )}
                              {editVehicleTypeIndex === obj.objectId ? (
                                <Tag>
                                  <InputGroup size="xs" width="auto">
                                    <InputLeftAddon>
                                      Vahicle Type
                                    </InputLeftAddon>
                                    <Select
                                      // value={obj.topColor}
                                      placeholder="Select Vehicle Type"
                                      onChange={(e) =>
                                        setNewVehicleTypeValue(e.target.value)
                                      }>
                                      <option value="auto">auto</option>
                                      <option value="bicycle">bicycle</option>
                                      <option value="bus">bus</option>
                                      <option value="car">car</option>
                                      <option value="carrier">carrier</option>
                                      <option value="motorbike">
                                        motorbike
                                      </option>
                                      <option value="truck">truck</option>
                                      <option value="van">van</option>
                                      <option value="un">un</option>
                                      <option value="others">others</option>
                                    </Select>
                                  </InputGroup>
                                  <Button
                                    onClick={() => {
                                      setEditVehicleTypeIndex("");
                                      handleUpdateVehicleType(
                                        obj.objectId,
                                        newVehicleTypeValue
                                      );
                                    }}
                                    size={"xs"}>
                                    {" "}
                                    <FaRegSave />
                                  </Button>
                                  <Button
                                    size={"xs"}
                                    onClick={() => {
                                      setEditVehicleTypeIndex("");
                                    }}>
                                    <SmallCloseIcon />
                                  </Button>
                                </Tag>
                              ) : (
                                obj.objectType === 2 && (
                                  <Tag>
                                    Vehicle Type : {obj.vehicleType}{" "}
                                    <Button
                                      size={"xs"}
                                      onClick={() => {
                                        setEditVehicleTypeIndex(obj.objectId);
                                      }}>
                                      <FaEdit />
                                    </Button>
                                  </Tag>
                                )
                              )}
                              {editVehicleColorIndex === obj.objectId ? (
                                <Tag>
                                  <InputGroup size="xs" width="auto">
                                    <InputLeftAddon>Top Color</InputLeftAddon>
                                    <Select
                                      // value={obj.topColor}
                                      placeholder="Select Vehicle color"
                                      onChange={(e) =>
                                        setNewVehicleColorValue(e.target.value)
                                      }>
                                      <option value="black">black</option>
                                      <option value="blue">blue</option>
                                      <option value="brown">brown</option>
                                      <option value="gray">gray</option>
                                      <option value="green">green</option>
                                      <option value="orange">orange</option>
                                      <option value="pink">pink</option>
                                      <option value="purple">purple</option>
                                      <option value="red">red</option>
                                      <option value="silver">silver</option>
                                      <option value="white">white</option>
                                      <option value="yellow">yellow</option>
                                      <option value="un">un</option>
                                      <option value="others">others</option>
                                    </Select>
                                  </InputGroup>
                                  <Button
                                    onClick={() => {
                                      setEditVehicleColorIndex("");
                                      handleUpdateVehicleColor(
                                        obj.objectId,
                                        newVehicleColorValue
                                      );
                                    }}
                                    size={"xs"}>
                                    {" "}
                                    <FaRegSave />
                                  </Button>
                                  <Button
                                    size={"xs"}
                                    onClick={() => {
                                      setEditVehicleColorIndex("");
                                    }}>
                                    <SmallCloseIcon />
                                  </Button>
                                </Tag>
                              ) : (
                                obj.objectType === 2 && (
                                  <Tag>
                                    Vehicle Color : {obj.vehicleColor}{" "}
                                    <Button
                                      size={"xs"}
                                      onClick={() => {
                                        setEditVehicleColorIndex(obj.objectId);
                                      }}>
                                      <FaEdit />
                                    </Button>
                                  </Tag>
                                )
                              )}
                              {editTopTypeIndex === obj.objectId ? (
                                <Tag>
                                  <InputGroup size="xs" width="auto">
                                    <InputLeftAddon>Top Type</InputLeftAddon>
                                    <Select
                                      placeholder="Select Top Type"
                                      onChange={(e) =>
                                        setNewTopTypeValue(e.target.value)
                                      }>
                                      <option value="jacket">jacket</option>
                                      <option value="shirt">shirt</option>
                                      <option value="tshirt">tshirt</option>
                                      <option value="un">un</option>
                                    </Select>
                                  </InputGroup>
                                  <Button
                                    onClick={() => {
                                      setEditTopTypeIndex("");
                                      handleUpdateTopType(
                                        obj.objectId,
                                        newTopTypeValue
                                      );
                                    }}
                                    size={"xs"}>
                                    {" "}
                                    <FaRegSave />
                                  </Button>
                                  <Button
                                    size={"xs"}
                                    onClick={() => {
                                      setEditTopTypeIndex("");
                                    }}>
                                    <SmallCloseIcon />
                                  </Button>
                                </Tag>
                              ) : (
                                obj.objectType === 1 && (
                                  <Tag>
                                    Top Type : {obj.topType}{" "}
                                    <Button
                                      size={"xs"}
                                      onClick={() => {
                                        setEditTopTypeIndex(obj.objectId);
                                      }}>
                                      <FaEdit />
                                    </Button>
                                  </Tag>
                                )
                              )}
                              {editBottomColorIndex === obj.objectId ? (
                                <Tag>
                                  <InputGroup size="xs" width="auto">
                                    <InputLeftAddon>
                                      Bottom Color
                                    </InputLeftAddon>
                                    <Select
                                      placeholder="Select Bottom color"
                                      onChange={(e) =>
                                        setNewBottomColorValue(e.target.value)
                                      }>
                                      <option value="black">black</option>
                                      <option value="blue">blue</option>
                                      <option value="green">green</option>
                                      <option value="red">red</option>
                                      <option value="white">white</option>
                                      <option value="yellow">yellow</option>
                                      <option value="un">un</option>
                                    </Select>
                                  </InputGroup>
                                  <Button
                                    onClick={() => {
                                      setEditBottomColorIndex("");
                                      handleUpdateBottomColor(
                                        obj.objectId,
                                        newBottomColorValue
                                      );
                                    }}
                                    size={"xs"}>
                                    {" "}
                                    <FaRegSave />
                                  </Button>
                                  <Button
                                    size={"xs"}
                                    onClick={() => {
                                      setEditBottomColorIndex("");
                                    }}>
                                    <SmallCloseIcon />
                                  </Button>
                                </Tag>
                              ) : (
                                obj.objectType === 1 && (
                                  <Tag>
                                    Bottom Color : {obj.bottomColor}{" "}
                                    <Button
                                      size={"xs"}
                                      onClick={() => {
                                        setEditBottomColorIndex(obj.objectId);
                                      }}>
                                      <FaEdit />
                                    </Button>
                                  </Tag>
                                )
                              )}
                              {editBottomTypeIndex === obj.objectId ? (
                                <Tag>
                                  <InputGroup size="xs" width="auto">
                                    <InputLeftAddon>Bottom Type</InputLeftAddon>
                                    <Select
                                      placeholder="Select Bottom Type"
                                      onChange={(e) =>
                                        setNewBottomTypeValue(e.target.value)
                                      }>
                                      <option value="longpants">
                                        longpants
                                      </option>
                                      <option value="shorts">shorts</option>
                                      <option value="skirt">skirt</option>
                                      <option value="un">un</option>
                                    </Select>
                                  </InputGroup>
                                  <Button
                                    onClick={() => {
                                      setEditBottomTypeIndex("");
                                      handleUpdateBottomType(
                                        obj.objectId,
                                        newBottomTypeValue
                                      );
                                    }}
                                    size={"xs"}>
                                    {" "}
                                    <FaRegSave />
                                  </Button>
                                  <Button
                                    size={"xs"}
                                    onClick={() => {
                                      setEditBottomTypeIndex("");
                                    }}>
                                    <SmallCloseIcon />
                                  </Button>
                                </Tag>
                              ) : (
                                obj.objectType === 1 && (
                                  <Tag>
                                    Bottom Type : {obj.bottomType}{" "}
                                    <Button
                                      size={"xs"}
                                      onClick={() => {
                                        setEditBottomTypeIndex(obj.objectId);
                                      }}>
                                      <FaEdit />
                                    </Button>
                                  </Tag>
                                )
                              )}
                              {editHasBagIndex === obj.objectId ? (
                                <Tag>
                                  <InputGroup size="xs" width="auto">
                                    <InputLeftAddon>Has Bag</InputLeftAddon>
                                    <Select
                                      placeholder="Select Has Bag"
                                      onChange={(e) =>
                                        setNewHasBagValue(e.target.value)
                                      }>
                                      <option value="yes">yes</option>
                                      <option value="no">no</option>
                                    </Select>
                                  </InputGroup>
                                  <Button
                                    onClick={() => {
                                      setEditHasBagIndex("");
                                      handleUpdateHasBag(
                                        obj.objectId,
                                        newHasBagValue
                                      );
                                    }}
                                    size={"xs"}>
                                    {" "}
                                    <FaRegSave />
                                  </Button>
                                  <Button
                                    size={"xs"}
                                    onClick={() => {
                                      setEditHasBagIndex("");
                                    }}>
                                    <SmallCloseIcon />
                                  </Button>
                                </Tag>
                              ) : (
                                obj.objectType === 1 && (
                                  <Tag>
                                    Has Bag : {obj.hasBag}{" "}
                                    <Button
                                      size={"xs"}
                                      onClick={() => {
                                        setEditHasBagIndex(obj.objectId);
                                      }}>
                                      <FaEdit />
                                    </Button>
                                  </Tag>
                                )
                              )}
                              {editHasHatIndex === obj.objectId ? (
                                <Tag>
                                  <InputGroup size="xs" width="auto">
                                    <InputLeftAddon>Has Hat</InputLeftAddon>
                                    <Select
                                      placeholder="Select Has Hat"
                                      onChange={(e) =>
                                        setNewHasHatValue(e.target.value)
                                      }>
                                      <option value="yes">yes</option>
                                      <option value="no">no</option>
                                    </Select>
                                  </InputGroup>
                                  <Button
                                    onClick={() => {
                                      setEditHasHatIndex("");
                                      handleUpdateHasHat(
                                        obj.objectId,
                                        newHasHatValue
                                      );
                                    }}
                                    size={"xs"}>
                                    {" "}
                                    <FaRegSave />
                                  </Button>
                                  <Button
                                    size={"xs"}
                                    onClick={() => {
                                      setEditHasHatIndex("");
                                    }}>
                                    <SmallCloseIcon />
                                  </Button>
                                </Tag>
                              ) : (
                                obj.objectType === 1 && (
                                  <Tag>
                                    Has Hat : {obj.hasHat}{" "}
                                    <Button
                                      size={"xs"}
                                      onClick={() => {
                                        setEditHasHatIndex(obj.objectId);
                                      }}>
                                      <FaEdit />
                                    </Button>
                                  </Tag>
                                )
                              )}

                              {editObjectTypeIndex === obj.objectId ? (
                                <Tag>
                                  <InputGroup size="xs" width="auto">
                                    <InputLeftAddon>Object Type</InputLeftAddon>
                                    <Select
                                      // value={
                                      //   obj.objectType === 2
                                      //     ? "Vehicle"
                                      //     : obj.objectType === 1
                                      //       ? "Human"
                                      //       : obj.objectType
                                      // }
                                      placeholder="Select Object Type"
                                      onChange={(e) =>
                                        setNewObjectTypeValue(
                                          Number(e.target.value)
                                        )
                                      }>
                                      <option value="1">Human</option>
                                      <option value="2">Vehicle</option>
                                      <option value="3">Others</option>
                                    </Select>
                                  </InputGroup>
                                  <Button
                                    onClick={() => {
                                      setEditObjectTypeIndex("");
                                      handleUpdateObjectType(
                                        obj.objectId,
                                        newObjectTypeValue
                                      );
                                    }}
                                    size={"xs"}>
                                    {" "}
                                    <FaRegSave />
                                  </Button>
                                  <Button
                                    size={"xs"}
                                    onClick={() => {
                                      setEditObjectTypeIndex("");
                                    }}>
                                    <SmallCloseIcon />
                                  </Button>
                                </Tag>
                              ) : (
                                <Tag>
                                  ObjectType :{" "}
                                  {obj.objectType === 2
                                    ? "Vehicle"
                                    : obj.objectType === 1
                                      ? "Human"
                                      : "Others"}{" "}
                                  <Button
                                    size={"xs"}
                                    onClick={() => {
                                      setEditObjectTypeIndex(obj.objectId);
                                    }}>
                                    <FaEdit />
                                  </Button>
                                </Tag>
                              )}

                              {editSexIndex === obj.objectId ? (
                                <Tag>
                                  <InputGroup size="xs" width="auto">
                                    <InputLeftAddon>Sex</InputLeftAddon>
                                    <Select
                                      placeholder="Select Sex"
                                      onChange={(e) =>
                                        setNewSexValue(e.target.value)
                                      }>
                                      <option value="male">male</option>
                                      <option value="female">female</option>
                                      <option value="un">un</option>
                                    </Select>
                                  </InputGroup>
                                  <Button
                                    onClick={() => {
                                      setEditSexIndex("");
                                      handleUpdateSex(
                                        obj.objectId,
                                        newSexValue
                                      );
                                    }}
                                    size={"xs"}>
                                    {" "}
                                    <FaRegSave />
                                  </Button>
                                  <Button
                                    size={"xs"}
                                    onClick={() => {
                                      setEditSexIndex("");
                                    }}>
                                    <SmallCloseIcon />
                                  </Button>
                                </Tag>
                              ) : (
                                obj.objectType === 1 && (
                                  <Tag>
                                    Sex : {obj.sex}{" "}
                                    <Button
                                      size={"xs"}
                                      onClick={() => {
                                        setEditSexIndex(obj.objectId);
                                      }}>
                                      <FaEdit />
                                    </Button>
                                  </Tag>
                                )
                              )}
                            </Stack>
                          </CardHeader>
                          <Divider />
                          <CardBody
                            style={{
                              display: "flex",
                              justifyContent: "center",
                              alignItems: "center",
                            }}>
                            {/* <AspectRatio ratio={133 / 392}> */}
                            <Image
                              _focus={{ outline: "none", shadow: "outline" }}
                              loading="lazy"
                              borderRadius="md"
                              // aspectRatio={1}
                              // src={`http://172.16.1.138:9983${obj.snap}`}
                              src={obj.snap}
                              // alt={tableProps.row.original.snapUrl}
                              fallbackSrc="https://via.placeholder.com/100"
                              // style={{ border: "1px solid orange", }}
                            />
                            {/* </AspectRatio> */}
                          </CardBody>
                        </Card>
                      </Box>
                    </PopoverContent>
                  </Popover>
                </React.Fragment>
              ))}
            </Grid>
          )}
        </CardBody>
        <CardFooter justify={"center"}>
          <HStack>
            <Button onClick={handlePrevPage} isDisabled={currentPage === 0}>
              {" "}
              <ChevronLeftIcon />{" "}
            </Button>
            <Text>Page : {currentPage + 1} </Text>
            <Button
              onClick={handleNextPage}
              isDisabled={sortedData?.length! < pageSize}>
              {" "}
              <ChevronRightIcon />{" "}
            </Button>
          </HStack>
        </CardFooter>
      </Card>
    </Box>
  );
}
