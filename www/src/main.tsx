import { Box, ChakraProvider } from "@chakra-ui/react";
import React from "react";
import ReactDOM from "react-dom/client";
import { Provider } from "react-redux";

import App from "./App";
import { store } from "./app/store";
// import i18n (needs to be bundled ;))
import { LanguageProvider } from "./context/LanguageContext";
import "./i18n";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <Provider store={store}>
      <LanguageProvider>
        <ChakraProvider>
          <Box h={"100vh"} p={0} m={0}>
            <App />
          </Box>
        </ChakraProvider>
      </LanguageProvider>
    </Provider>
  </React.StrictMode>
);
