import { Locale } from "date-fns";
import { arSA, bn, enUS, es, th } from "date-fns/locale";
import {
  ReactElement,
  createContext,
  useCallback,
  useContext,
  useReducer,
} from "react";
// import rtl from "stylis-plugin-rtl";
import { RtlProvider } from "../RtlProvider.tsx";
import i18n, { getLanguage, getLngDir } from "../i18n";

type ChildrenType = {
  children?: ReactElement | ReactElement[] | undefined;
};

export const LanguageOptions: {
  value: string;
  label: string;
}[] = [
  { value: "en", label: "en" },
  { value: "sa", label: "sa" },
  { value: "bn", label: "bn" },
  { value: "es", label: "es" },
  { value: "th", label: "th" },
];
export const getDateFnsLocale = (lng: string): Locale => {
  switch (lng) {
    case "en":
      return enUS;
    case "sa":
      return arSA;
    case "bn":
      return bn;
    case "es":
      return es;
    case "th":
      return th;
  }
  return enUS;
};

type StateType = {
  locale: string;
  direction: "ltr" | "rtl";
  date_fns_locale: Locale;
};

const enum LANG_REDUCER_ACTION_TYPE {
  CHANGE_LOCALE,
}

type ReducerActionType = {
  type: LANG_REDUCER_ACTION_TYPE;
  payload?: string;
};

const initialLanguage: StateType = {
  locale: getLanguage(),
  direction: getLngDir(getLanguage()),
  date_fns_locale: getDateFnsLocale(getLanguage()),
};

const languageReducer = (
  language: StateType,
  action: ReducerActionType
): StateType => {
  switch (action.type) {
    case LANG_REDUCER_ACTION_TYPE.CHANGE_LOCALE:
      const l = action.payload ?? initialLanguage.locale;
      i18n.changeLanguage(l);
      return {
        ...language,
        locale: l,
        direction: getLngDir(l),
        date_fns_locale: getDateFnsLocale(l),
      };
    default:
      throw Error("Unknown action: " + action.type);
  }
};

// const options = {
//   rtl: { key: "css-ar", stylisPlugins: [rtl] },
//   ltr: { key: "css-en" },
// };

const useLanguageContext = (initState: StateType) => {
  const [language, dispatch] = useReducer(languageReducer, initState);
  const handleLocaleChange = useCallback((e: string) => {
    dispatch({
      type: LANG_REDUCER_ACTION_TYPE.CHANGE_LOCALE,
      payload: e,
    });
  }, []);
  return { language, handleLocaleChange };
};

type UseLanguageContextType = ReturnType<typeof useLanguageContext>;

const initContextState: UseLanguageContextType = {
  language: initialLanguage,
  handleLocaleChange: () => {},
};

const LanguageContext = createContext<UseLanguageContextType>(initContextState);

export const useLanguage = (): UseLanguageHookType => {
  const { language, handleLocaleChange } = useContext(LanguageContext);
  return { language, handleLocaleChange };
};

export const LanguageProvider = ({ children }: ChildrenType): ReactElement => {
  // const { language } = useLanguage();
  // const [cache, setCache] = useState(createCache(options[language.direction]));
  // useEffect(() => {
  //   setCache(createCache(options[language.direction]));
  // }, [language]);
  return (
    <LanguageContext.Provider value={useLanguageContext(initialLanguage)}>
      <RtlProvider>{children}</RtlProvider>
    </LanguageContext.Provider>
  );
};

type UseLanguageHookType = {
  language: StateType;
  handleLocaleChange: (e: string) => void;
};
