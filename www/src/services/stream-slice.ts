import getFormData from "../utils/utils";
import { graphApi as api } from "./graph-api";
import {
  PostSdpApiResponse,
  PostSdpApiArg,
  PostSdpFormDataApiResponse,
  PostSdpFormDataApiArg,
} from "./graphApiGen";

const injectedRtkApi = api.injectEndpoints({
  endpoints: (build) => ({
    postSdp: build.mutation<PostSdpApiResponse, PostSdpApiArg>({
      query: (queryArg) => ({
        url: `/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/live/${queryArg.liveOrRecording}/stream/${queryArg.streamId}/timestamp/${queryArg.timeStamp}/webrtc`,
        method: "POST",
        body: getFormData(
          queryArg.bodyPostSdpStreamSiteSiteIdChannelChannelIdAppAppIdLiveLiveOrRecordingStreamStreamIdTimestampTimeStampWebrtcPost
        ),
        responseHandler: "text",
      }),
    }),
    postSdpFormData: build.mutation<
      PostSdpFormDataApiResponse,
      PostSdpFormDataApiArg
    >({
      query: (queryArg) => ({
        url: `/v2/stream/site/${queryArg.siteId}/channel/${queryArg.channelId}/app/${queryArg.appId}/live/${queryArg.liveOrRecording}/stream/${queryArg.streamId}/timestamp/${queryArg.timeStamp}/webrtc`,
        method: "POST",
        body: getFormData(
          queryArg.bodyPostSdpFormDataV2StreamSiteSiteIdChannelChannelIdAppAppIdLiveLiveOrRecordingStreamStreamIdTimestampTimeStampWebrtcPost
        ),
        // responseHandler: "text",
      }),
    }),
  }),
  overrideExisting: true,
});
export const { usePostSdpMutation, usePostSdpFormDataMutation } =
  injectedRtkApi;
