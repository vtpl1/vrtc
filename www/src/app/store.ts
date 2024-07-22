import { configureStore } from "@reduxjs/toolkit";
import { graphApi } from "../services/graph-api";

export const store = configureStore({
  reducer: {
    [graphApi.reducerPath]: graphApi.reducer,
  },
  middleware: (getDefaultMiddleware) => {
    return getDefaultMiddleware().concat(graphApi.middleware);
  },
});

export type AppDispatch = typeof store.dispatch;
export type RootState = ReturnType<typeof store.getState>;
