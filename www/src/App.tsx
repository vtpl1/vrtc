import { Box, HStack, Text, Radio, RadioGroup, Tag } from "@chakra-ui/react";
import { Route, BrowserRouter as Router, Routes } from "react-router-dom";
import GridSelector from "./components/GridSelector";
import { useEffect, useState } from "react";
import { useDebounce } from "@uidotdev/usehooks";
import { ChevronDownIcon } from "@chakra-ui/icons";
import Events from "./components/Events/Events";
import Dashboard from "./components/Dashboard";
import { useTranslation } from "react-i18next";
import { useLanguage } from "./context/LanguageContext";
import Login from "./components/Login";
import Layout from "./components/Layout";
import Missing from "./components/Missing";

function App() {
  const { t } = useTranslation();
  const { language } = useLanguage();
  useEffect(() => {
    document.title = "Videonetics " + t("title");
  }, [language, t]);
  const [width, setWidth] = useState(window.innerWidth);
  console.log("Width", width);
  return (
    <Router>
      <Routes>
        <Route path="login" element={<Login />} />
        <Route path="" element={<Layout />}>
          <Route index element={<Dashboard />} />
          <Route path="/:locale" element={<Dashboard />} />
          <Route path="/:locale/:dashboard" element={<Dashboard />} />
          <Route path="*" element={<Missing />} />
        </Route>
      </Routes>
    </Router>
  );
}

export default App;
