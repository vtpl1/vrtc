import createCache from "@emotion/cache";
import { CacheProvider } from "@emotion/react";
import { ReactElement, useEffect, useState } from "react";
import rtl from "stylis-plugin-rtl";
import { useLanguage } from "./context/LanguageContext";

// NB: A unique `key` is important for it to work!
const options = {
  rtl: { key: "css-ar", stylisPlugins: [rtl] },
  ltr: { key: "css-en" },
};

type ChildrenType = {
  children?: ReactElement | ReactElement[] | undefined;
};

export const RtlProvider = ({ children }: ChildrenType): ReactElement => {
  const { language } = useLanguage();
  const [cache, setCache] = useState(createCache(options[language.direction]));
  useEffect(() => {
    setCache(createCache(options[language.direction]));
  }, [language]);
  return <CacheProvider value={cache} children={children} />;
};
