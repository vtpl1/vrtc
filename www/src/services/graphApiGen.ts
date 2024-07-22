import { graphApi as api } from "./graph-api";
const injectedRtkApi = api.injectEndpoints({
  endpoints: (build) => ({
    postSdp: build.mutation<PostSdpApiResponse, PostSdpApiArg>({
      query: (queryArg) => ({
        url: `/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/live/${queryArg.liveOrRecording}/stream/${queryArg.streamId}/timestamp/${queryArg.timeStamp}/webrtc`,
        method: "POST",
        body: queryArg.bodyPostSdpStreamSiteSiteIdChannelChannelIdAppAppIdLiveLiveOrRecordingStreamStreamIdTimestampTimeStampWebrtcPost,
      }),
    }),
    postSdpFormData: build.mutation<
      PostSdpFormDataApiResponse,
      PostSdpFormDataApiArg
    >({
      query: (queryArg) => ({
        url: `/v2/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/live/${queryArg.liveOrRecording}/stream/${queryArg.streamId}/timestamp/${queryArg.timeStamp}/webrtc`,
        method: "POST",
        body: queryArg.bodyPostSdpFormDataV2StreamSiteSiteIdChannelChannelIdAppAppIdLiveLiveOrRecordingStreamStreamIdTimestampTimeStampWebrtcPost,
      }),
    }),
    pauseStream: build.mutation<PauseStreamApiResponse, PauseStreamApiArg>({
      query: (queryArg) => ({
        url: `/v2/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/archive/sessionid/${queryArg.sessionId}/webrtc/pause`,
        method: "POST",
      }),
    }),
    resumeStream: build.mutation<ResumeStreamApiResponse, ResumeStreamApiArg>({
      query: (queryArg) => ({
        url: `/v2/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/archive/sessionid/${queryArg.sessionId}/webrtc/resume`,
        method: "POST",
      }),
    }),
    seekStream: build.mutation<SeekStreamApiResponse, SeekStreamApiArg>({
      query: (queryArg) => ({
        url: `/v2/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/archive/sessionid/${queryArg.sessionId}/webrtc/seek/${queryArg.timeStamp}`,
        method: "POST",
      }),
    }),
    forwardStream: build.mutation<
      ForwardStreamApiResponse,
      ForwardStreamApiArg
    >({
      query: (queryArg) => ({
        url: `/v2/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/archive/sessionid/${queryArg.sessionId}/webrtc/forward/speed/${queryArg.speed}`,
        method: "POST",
      }),
    }),
    backwardStream: build.mutation<
      BackwardStreamApiResponse,
      BackwardStreamApiArg
    >({
      query: (queryArg) => ({
        url: `/v2/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/archive/sessionid/${queryArg.sessionId}/webrtc/backward/speed/${queryArg.speed}`,
        method: "POST",
      }),
    }),
    speed: build.mutation<SpeedApiResponse, SpeedApiArg>({
      query: (queryArg) => ({
        url: `/v2/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/archive/sessionid/${queryArg.sessionId}/webrtc/speed/${queryArg.speed}`,
        method: "POST",
      }),
    }),
    normalSpeed: build.mutation<NormalSpeedApiResponse, NormalSpeedApiArg>({
      query: (queryArg) => ({
        url: `/v2/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/archive/sessionid/${queryArg.sessionId}/webrtc/normalplay/speed/${queryArg.speed}`,
        method: "POST",
      }),
    }),
    frameByFrameForwardStream: build.mutation<
      FrameByFrameForwardStreamApiResponse,
      FrameByFrameForwardStreamApiArg
    >({
      query: (queryArg) => ({
        url: `/v2/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/archive/sessionid/${queryArg.sessionId}/webrtc/forward/frame_by_frame`,
        method: "POST",
      }),
    }),
    frameByFrameBackwardStream: build.mutation<
      FrameByFrameBackwardStreamApiResponse,
      FrameByFrameBackwardStreamApiArg
    >({
      query: (queryArg) => ({
        url: `/v2/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/archive/sessionid/${queryArg.sessionId}/webrtc/backward/frame_by_frame`,
        method: "POST",
      }),
    }),
    getMetadataBars: build.query<
      GetMetadataBarsApiResponse,
      GetMetadataBarsApiArg
    >({
      query: (queryArg) => ({
        url: `/metadata/bar`,
        params: {
          site_id: queryArg.siteId,
          channel_id: queryArg.channelId,
          start_time: queryArg.startTime,
          end_time: queryArg.endTime,
        },
      }),
    }),
    getMetadataEventBars: build.mutation<
      GetMetadataEventBarsApiResponse,
      GetMetadataEventBarsApiArg
    >({
      query: (queryArg) => ({
        url: `/metadata/eventbar`,
        method: "POST",
        body: queryArg.zoneInfo,
        params: {
          site_id: queryArg.siteId,
          channel_id: queryArg.channelId,
          start_time: queryArg.startTime,
          end_time: queryArg.endTime,
        },
      }),
    }),
    getMetadataCount: build.query<
      GetMetadataCountApiResponse,
      GetMetadataCountApiArg
    >({
      query: (queryArg) => ({
        url: `/metadata/count`,
        params: {
          site_id: queryArg.siteId,
          channel_id: queryArg.channelId,
          start_time: queryArg.startTime,
          end_time: queryArg.endTime,
        },
      }),
    }),
    readStartEndDate: build.query<
      ReadStartEndDateApiResponse,
      ReadStartEndDateApiArg
    >({
      query: () => ({ url: `/metadata/start_end_date` }),
    }),
    getMetadata: build.query<GetMetadataApiResponse, GetMetadataApiArg>({
      query: (queryArg) => ({
        url: `/metadata/`,
        params: {
          site_id: queryArg.siteId,
          channel_id: queryArg.channelId,
          start_time: queryArg.startTime,
          end_time: queryArg.endTime,
          skip: queryArg.skip,
          limit: queryArg.limit,
        },
      }),
    }),
    getRecordings: build.query<GetRecordingsApiResponse, GetRecordingsApiArg>({
      query: (queryArg) => ({
        url: `/recordings/bar`,
        params: {
          site_id: queryArg.siteId,
          channel_id: queryArg.channelId,
          start_time: queryArg.startTime,
          end_time: queryArg.endTime,
          skip: queryArg.skip,
          limit: queryArg.limit,
        },
      }),
    }),
    getAttributeEvents: build.query<
      GetAttributeEventsApiResponse,
      GetAttributeEventsApiArg
    >({
      query: (queryArg) => ({
        url: `/recordings/attributeEvents`,
        params: {
          channel_id: queryArg.channelId,
          start_time: queryArg.startTime,
          end_time: queryArg.endTime,
          track_id: queryArg.trackId,
        },
      }),
    }),
    getAttributeEvent: build.query<
      GetAttributeEventApiResponse,
      GetAttributeEventApiArg
    >({
      query: (queryArg) => ({
        url: `/event/attribute_event`,
        params: {
          site_id: queryArg.siteId,
          channel_id: queryArg.channelId,
          start_time: queryArg.startTime,
          end_time: queryArg.endTime,
          object_type: queryArg.objectType,
          has_bag: queryArg.hasBag,
          has_hat: queryArg.hasHat,
          top_type: queryArg.topType,
          top_color: queryArg.topColor,
          bottom_type: queryArg.bottomType,
          bottom_color: queryArg.bottomColor,
          sex: queryArg.sex,
          vehicle_type: queryArg.vehicleType,
          vehicle_color: queryArg.vehicleColor,
          edited: queryArg.edited,
          skip: queryArg.skip,
          limit: queryArg.limit,
        },
      }),
    }),
    updateAttributeEvent: build.mutation<
      UpdateAttributeEventApiResponse,
      UpdateAttributeEventApiArg
    >({
      query: (queryArg) => ({
        url: `/event/update_attribute_event`,
        method: "PUT",
        body: queryArg.value,
        params: { key: queryArg.key },
      }),
    }),
  }),
  overrideExisting: false,
});
export { injectedRtkApi as graphApiGen };
export type PostSdpApiResponse = /** status 200 Successful Response */ any;
export type PostSdpApiArg = {
  siteId: number;
  channelId: number;
  appId: number;
  liveOrRecording: number;
  streamId: number;
  timeStamp: number;
  bodyPostSdpStreamSiteSiteIdChannelChannelIdAppAppIdLiveLiveOrRecordingStreamStreamIdTimestampTimeStampWebrtcPost: BodyPostSdpStreamSiteSiteIdChannelChannelIdAppAppIdLiveLiveOrRecordingStreamStreamIdTimestampTimeStampWebrtcPost;
};
export type PostSdpFormDataApiResponse =
  /** status 200 Successful Response */ Sdp;
export type PostSdpFormDataApiArg = {
  siteId: number;
  channelId: number;
  appId: number;
  liveOrRecording: number;
  streamId: number;
  timeStamp: number;
  bodyPostSdpFormDataV2StreamSiteSiteIdChannelChannelIdAppAppIdLiveLiveOrRecordingStreamStreamIdTimestampTimeStampWebrtcPost: BodyPostSdpFormDataV2StreamSiteSiteIdChannelChannelIdAppAppIdLiveLiveOrRecordingStreamStreamIdTimestampTimeStampWebrtcPost;
};
export type PauseStreamApiResponse =
  /** status 200 Successful Response */ Session;
export type PauseStreamApiArg = {
  siteId: number;
  channelId: number;
  appId: number;
  sessionId: string;
};
export type ResumeStreamApiResponse =
  /** status 200 Successful Response */ Session;
export type ResumeStreamApiArg = {
  siteId: number;
  channelId: number;
  appId: number;
  sessionId: string;
};
export type SeekStreamApiResponse =
  /** status 200 Successful Response */ Session;
export type SeekStreamApiArg = {
  siteId: number;
  channelId: number;
  appId: number;
  sessionId: string;
  timeStamp: number;
};
export type ForwardStreamApiResponse =
  /** status 200 Successful Response */ Session;
export type ForwardStreamApiArg = {
  siteId: number;
  channelId: number;
  appId: number;
  sessionId: string;
  speed: number;
};
export type BackwardStreamApiResponse =
  /** status 200 Successful Response */ Session;
export type BackwardStreamApiArg = {
  siteId: number;
  channelId: number;
  appId: number;
  sessionId: string;
  speed: number;
};
export type SpeedApiResponse = /** status 200 Successful Response */ Session;
export type SpeedApiArg = {
  siteId: number;
  channelId: number;
  appId: number;
  sessionId: string;
  speed: number;
};
export type NormalSpeedApiResponse =
  /** status 200 Successful Response */ Session;
export type NormalSpeedApiArg = {
  siteId: number;
  channelId: number;
  appId: number;
  sessionId: string;
  speed: number;
};
export type FrameByFrameForwardStreamApiResponse =
  /** status 200 Successful Response */ Session;
export type FrameByFrameForwardStreamApiArg = {
  siteId: number;
  channelId: number;
  appId: number;
  sessionId: string;
};
export type FrameByFrameBackwardStreamApiResponse =
  /** status 200 Successful Response */ Session;
export type FrameByFrameBackwardStreamApiArg = {
  siteId: number;
  channelId: number;
  appId: number;
  sessionId: string;
};
export type GetMetadataBarsApiResponse =
  /** status 200 Successful Response */ MetadataBar;
export type GetMetadataBarsApiArg = {
  siteId: number;
  channelId: number;
  startTime: number;
  endTime: number;
};
export type GetMetadataEventBarsApiResponse =
  /** status 200 Successful Response */ MetadataEventBar;
export type GetMetadataEventBarsApiArg = {
  siteId: number;
  channelId: number;
  startTime: number;
  endTime: number;
  zoneInfo: ZoneInfo;
};
export type GetMetadataCountApiResponse =
  /** status 200 Successful Response */ VCount;
export type GetMetadataCountApiArg = {
  siteId: number;
  channelId: number;
  startTime: number;
  endTime: number;
};
export type ReadStartEndDateApiResponse =
  /** status 200 Successful Response */ VStartEndDate;
export type ReadStartEndDateApiArg = void;
export type GetMetadataApiResponse =
  /** status 200 Successful Response */ FrameInfo[];
export type GetMetadataApiArg = {
  siteId: number;
  channelId: number;
  startTime: number;
  endTime: number;
  skip?: number;
  limit?: number;
};
export type GetRecordingsApiResponse =
  /** status 200 Successful Response */ RecordingBar;
export type GetRecordingsApiArg = {
  siteId: number;
  channelId: number;
  startTime: number;
  endTime: number;
  skip?: number;
  limit?: number;
};
export type GetAttributeEventsApiResponse =
  /** status 200 Successful Response */ AttributeEvents;
export type GetAttributeEventsApiArg = {
  channelId: number;
  startTime: number;
  endTime: number;
  trackId?: string[];
};
export type GetAttributeEventApiResponse =
  /** status 200 Successful Response */ AttributeEvent[];
export type GetAttributeEventApiArg = {
  siteId: number;
  channelId: number;
  startTime: number;
  endTime: number;
  objectType?: number;
  hasBag?: string;
  hasHat?: string;
  topType?: string;
  topColor?: string;
  bottomType?: string;
  bottomColor?: string;
  sex?: string;
  vehicleType?: string;
  vehicleColor?: string;
  edited?: string;
  skip?: number;
  limit?: number;
};
export type UpdateAttributeEventApiResponse =
  /** status 200 Successful Response */ boolean;
export type UpdateAttributeEventApiArg = {
  key: string;
  value: object;
};
export type ValidationError = {
  loc: (string | number)[];
  msg: string;
  type: string;
};
export type HttpValidationError = {
  detail?: ValidationError[];
};
export type BodyPostSdpStreamSiteSiteIdChannelChannelIdAppAppIdLiveLiveOrRecordingStreamStreamIdTimestampTimeStampWebrtcPost =
  {
    data: string;
  };
export type Sdp = {
  type: string;
  sessionId: string;
  sdp: string;
};
export type BodyPostSdpFormDataV2StreamSiteSiteIdChannelChannelIdAppAppIdLiveLiveOrRecordingStreamStreamIdTimestampTimeStampWebrtcPost =
  {
    data: string;
  };
export type Session = {
  sessionId: string;
};
export type Bar = {
  startTime: number;
  endTime: number;
};
export type MetadataBar = {
  siteId: number;
  channelId: number;
  startTime: number;
  endTime: number;
  humanBars: Bar[];
  vehicleBars: Bar[];
};
export type MetadataEventBar = {
  siteId: number;
  channelId: number;
  startTime: number;
  endTime: number;
  eventBars: Bar[];
};
export type Vertices = {
  x: number;
  y: number;
};
export type ZoneInfo = {
  zoneId: number;
  zoneType: string;
  vertices: Vertices[];
};
export type VCount = {
  count: number;
};
export type VStartEndDate = {
  start: number;
  end: number;
};
export type ObjectInfo = {
  x: number;
  y: number;
  w: number;
  h: number;
  t: number;
  c: number;
  i: number;
};
export type FrameInfo = {
  siteId: number;
  channelId: number;
  timeStamp: number;
  timeStampEnd: number;
  vehicleCount: number;
  peopleCount: number;
  refWidth?: number | null;
  refHeight?: number | null;
  objectList?: ObjectInfo[] | null;
};
export type RecordingBar = {
  siteId: number;
  channelId: number;
  startTime: number;
  endTime: number;
  bars: Bar[];
};
export type MetaAttributeEvent = {
  objecttype: number;
  estimatedheight: number;
  toptype: string;
  topcolor: string;
  bottomtype: string;
  bottomcolor: string;
  sex: string;
  presenceofbag: boolean;
  clothingpattern: string;
  presenceofheadedress: boolean;
  typeofheaddress: string;
  associatedobject: string;
  presenceoflongsleeve: boolean;
  vehicletype: string;
  vehiclecolor: string;
  eventtime: number;
  channelid: number;
  appid: number;
  trackid: number;
};
export type AttributeEvents = {
  trackId: string[];
  channelId: number;
  startTime: number;
  endTime: number;
  metaAttributeEvent: MetaAttributeEvent[];
};
export type ObjectType = 1 | 2 | 3;
export type SexType = "male" | "female" | "un";
export type TopType = "jacket" | "shirt" | "tshirt" | "un";
export type BottomType = "longpants" | "shorts" | "skirt" | "un";
export type TopColor =
  | "black"
  | "blue"
  | "green"
  | "red"
  | "white"
  | "yellow"
  | "un";
export type VehicleColor =
  | "black"
  | "blue"
  | "brown"
  | "gray"
  | "green"
  | "orange"
  | "pink"
  | "purple"
  | "red"
  | "silver"
  | "white"
  | "yellow"
  | "un"
  | "others";
export type VehicleType =
  | "auto"
  | "bicycle"
  | "bus"
  | "car"
  | "carrier"
  | "motorbike"
  | "truck"
  | "un"
  | "van"
  | "others";
export type BottomColor =
  | "black"
  | "blue"
  | "green"
  | "red"
  | "white"
  | "yellow"
  | "un";
export type HasBag = "yes" | "no";
export type HasHat = "yes" | "no";
export type AttributeEvent = {
  objectId: string;
  siteId: number;
  channelId: number;
  startTimeStamp: number;
  endTimeStamp: number;
  objectType: ObjectType;
  sex: SexType;
  topType: TopType;
  bottomType: BottomType;
  topColor: TopColor;
  vehicleColor: VehicleColor;
  vehicleType: VehicleType;
  bottomColor: BottomColor;
  hasBag: HasBag;
  hasHat: HasHat;
  snap: string;
  trackId: number;
  edited?: string | null;
};
export const {
  usePostSdpMutation,
  usePostSdpFormDataMutation,
  usePauseStreamMutation,
  useResumeStreamMutation,
  useSeekStreamMutation,
  useForwardStreamMutation,
  useBackwardStreamMutation,
  useSpeedMutation,
  useNormalSpeedMutation,
  useFrameByFrameForwardStreamMutation,
  useFrameByFrameBackwardStreamMutation,
  useGetMetadataBarsQuery,
  useGetMetadataEventBarsMutation,
  useGetMetadataCountQuery,
  useReadStartEndDateQuery,
  useGetMetadataQuery,
  useGetRecordingsQuery,
  useGetAttributeEventsQuery,
  useGetAttributeEventQuery,
  useUpdateAttributeEventMutation,
} = injectedRtkApi;
