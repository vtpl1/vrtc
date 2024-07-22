import { PayloadAction, createSlice } from "@reduxjs/toolkit";
import { useMemo } from "react";
import { RootState } from "../../app/store";
import { graphApi as api } from "../../services/graph-api";
// import { LoginApiResponse, Token } from "../../services/graphApiGen";

export type User = {
  id?: string;
  fullName?: string;
};

export type AuthState = {
  user: User | null;
  // token: Token | null;
};

export type LoginRequest = {
  username: string;
  password: string;
  captcha: string;
};
const initialState: AuthState = { user: null /*token: null*/ };

// const injectedRtkApi = api.injectEndpoints({
//   endpoints: (build) => ({
//     login: build.mutation<LoginApiResponse, FormData>({
//       query: (formData) => ({
//         url: `/token/`,
//         method: "POST",
//         body: formData,
//         formData: true,
//       }),
//     }),
//   }),
//   overrideExisting: true,
// });

// const injectedRtkApi = api.injectEndpoints({
//   endpoints: (builder) => ({
//     login: builder.mutation<User, LoginRequest>({
//       query: (login_request) => ({
//         url: "/token",
//         method: "POST",
//         credentials: "include",
//         body: {
//           username: login_request.username,
//           password: encryptPassword(login_request.password),
//           captcha: login_request.captcha,
//         },
//         headers: {
//           "Content-Type": "application/json",
//           Accept: "application/json",
//         },
//       }),
//       transformResponse: (responseData: User) => {
//         console.debug("login retruns: " + JSON.stringify(responseData));
//         if (responseData) {
//           return responseData;
//         }
//         return initialState;
//       },
//     }),
//     signout: builder.mutation({
//       query: () => ({
//         url: "/signout",
//         method: "GET",
//         credentials: "include",
//         headers: {
//           "Content-Type": "application/json",
//           Accept: "application/json",
//         },
//       }),
//       transformResponse: (responseData) => {
//         console.log("signout retruns: " + JSON.stringify(responseData));
//         return responseData;
//       },
//     }),
//   }),
// });

// const authSlice = createSlice({
//   name: "auth",
//   initialState: {
//     user: null,
//   } as AuthState,
//   reducers: {
//     setCredentials: (
//       state,
//       {
//         payload: { user, token },
//       }: PayloadAction<{ user: User | null; token: Token | null }>
//     ) => {
//       console.log("setCredentials called + " + user);
//       state.user = user;
//       state.token = token;
//     },
//     signOut: (state) => {
//       state.user = null;
//       state.token = null;
//     },
//   },
//   extraReducers(builder) {
//     builder.addMatcher(
//       injectedRtkApi.endpoints.login.matchFulfilled,
//       (state, { payload }) => {
//         state.token = payload;
//         // state.user = payload.user;
//       }
//     );
//   },
// });
// const encryptPassword = (password: string): string => {
//   return sha512(
//     sha512(password) + "{" + Cookies.get("bd_eps_csrf_token") + "}"
//   ).toString();
// };

// export const { setCredentials, signOut } = authSlice.actions;
// export default authSlice.reducer;
// export const selectCurrentUser = (state: RootState) => state.auth.user;
// export const selectCurrentToken = (state: RootState) => state.auth.token;
export const useAuth = () => {
  // const user = useAppSelector(selectCurrentUser);
  // const { data } = useReadUsersMeQuery();

  const user: User = { fullName: "admin1", id: "admin" };
  return useMemo(() => ({ user }), [user]);
};
// export const {
//   useLoginMutation,
//   // useSignoutMutation
// } = injectedRtkApi;

// export const useLogin = (userId: string, pass: string): User => {
//   const [login, result] = useLoginMutation();

//   return {}
// }
