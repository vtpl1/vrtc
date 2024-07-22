import {
  BaseQueryApi,
  FetchArgs,
  createApi,
  fetchBaseQuery,
} from "@reduxjs/toolkit/query/react";

// https://redux-toolkit.js.org/rtk-query/usage/examples

export const GRAPH_BASE_URL = "/api";

const baseQuery = fetchBaseQuery({
  baseUrl: GRAPH_BASE_URL,
  timeout: 200000,
  credentials: "include",
});

export const baseQueryWithReauth = async (
  args: string | FetchArgs,
  api: BaseQueryApi,
  extraOptions: object
) => {
  const result = await baseQuery(args, api, extraOptions);
  return result;
};

export const graphApi = createApi({
  reducerPath: "graph",
  baseQuery: baseQuery,
  endpoints: () => ({}),
});
