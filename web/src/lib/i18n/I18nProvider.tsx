import { createContext, useCallback, useContext, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { IntlProvider, ReactIntlErrorCode } from "react-intl";
import type { IntlConfig } from "react-intl";
import { I18nProvider as AriaI18nProvider } from "@react-aria/i18n";

import type { User } from "@/types";
import { PSEUDO_TAG, directionFor, listedLanguages, loadMessages } from "./languages";
import type { CompiledCatalog } from "./languages";
import { resolveLanguage } from "./resolve";
import { readLanguageStorage, writeLanguageStorage } from "./storage";

declare global {
  interface Window {
    // Present only in builds that list the pseudo-language (spec 019 T100).
    gameplaneI18nTest?: { setOverride: (tag: string | null) => void };
  }
}

export interface I18nContextValue {
  language: string;
  setLanguage: (tag: string | null) => void;
  direction: "ltr" | "rtl";
}

// Outside a provider (tests, isolated renders) the dashboard stays English.
const FALLBACK: I18nContextValue = {
  language: "en",
  setLanguage: () => {},
  direction: "ltr",
};

const I18nContext = createContext<I18nContextValue | null>(null);

export function useLanguage(): I18nContextValue {
  return useContext(I18nContext) ?? FALLBACK;
}

// onIntlError keeps the console quiet: missing translations are expected (react-intl
// falls back to defaultMessage), and other formatting problems are only worth a
// warning while developing.
// IntlErrorArg is the union react-intl passes to onError (the IntlError subclasses).
type IntlErrorArg = Parameters<NonNullable<IntlConfig["onError"]>>[0];

export function onIntlError(err: IntlErrorArg): void {
  if (err.code === ReactIntlErrorCode.MISSING_TRANSLATION) return;
  if (import.meta.env.DEV) console.warn(err);
}

export interface I18nProviderProps {
  children: ReactNode;
  userLanguage?: string | null;
  instanceDefault?: string | null;
}

interface LoadedMessages {
  tag: string;
  messages: CompiledCatalog;
}

export function I18nProvider({ children, userLanguage, instanceDefault }: I18nProviderProps) {
  const [choice, setChoice] = useState<string | null>(() => readLanguageStorage().choice);
  const listed = useMemo(() => listedLanguages().map((row) => row.tag), []);

  // Test override (spec 019 T100, OD-018): builds that list the pseudo-language
  // (VITE_I18N_PSEUDO=1, never a release) expose window.gameplaneI18nTest so the
  // en-XA screenshot run can switch the language right before each capture.
  const pseudoBuild = listed.includes(PSEUDO_TAG);
  const [override, setOverride] = useState<string | null>(null);

  const resolved = resolveLanguage({
    userChoice: userLanguage,
    browserChoice: choice,
    navigatorLanguages: navigator.languages,
    instanceDefault,
    listed,
  });
  const language = pseudoBuild && override !== null && listed.includes(override) ? override : resolved;
  const direction = directionFor(language);

  const [loaded, setLoaded] = useState<LoadedMessages>({ tag: "en", messages: {} });

  useEffect(() => {
    let cancelled = false;
    void loadMessages(language)
      .then((messages) => {
        if (!cancelled) setLoaded({ tag: language, messages: messages ?? {} });
      })
      .catch(() => {
        // A failed chunk leaves the English defaultMessage text in place.
        if (!cancelled) setLoaded({ tag: language, messages: {} });
      });
    return () => {
      cancelled = true;
    };
  }, [language]);

  useEffect(() => {
    document.documentElement.lang = language;
    document.documentElement.dir = direction;
    writeLanguageStorage({ applied: language });
  }, [language, direction]);

  useEffect(() => {
    if (!pseudoBuild) return undefined;
    window.gameplaneI18nTest = { setOverride };
    return () => {
      delete window.gameplaneI18nTest;
    };
  }, [pseudoBuild]);

  // data-i18n-loaded names the language whose catalog is in place, so a test
  // can wait for a language switch to finish rendering.
  useEffect(() => {
    document.documentElement.dataset.i18nLoaded = loaded.tag;
  }, [loaded.tag]);

  const setLanguage = useCallback((tag: string | null) => {
    writeLanguageStorage({ choice: tag });
    setChoice(tag);
  }, []);

  const value = useMemo<I18nContextValue>(
    () => ({ language, setLanguage, direction }),
    [language, setLanguage, direction],
  );

  // Children are never remounted on a language change: while the new catalog
  // loads, the previous messages stay in place.
  return (
    <I18nContext.Provider value={value}>
      <IntlProvider locale={language} defaultLocale="en" messages={loaded.messages} onError={onIntlError}>
        <AriaI18nProvider locale={language}>{children}</AriaI18nProvider>
      </IntlProvider>
    </I18nContext.Provider>
  );
}

// useCachedUserLanguage returns the saved language on the cached ["me"]
// profile. It only observes the query cache and never starts a fetch: the
// route guards own /users/me, and this hook re-renders when that cache entry
// changes (login, a later language picker invalidating ["me"]).
function useCachedUserLanguage(): string | null | undefined {
  const queryClient = useQueryClient();
  const cache = queryClient.getQueryCache();
  const subscribe = useCallback((onChange: () => void) => cache.subscribe(onChange), [cache]);
  const snapshot = () => queryClient.getQueryData<User>(["me"])?.preferences?.language;
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}

// AppI18nProvider feeds I18nProvider the two server-side inputs of the
// resolution order (spec 019, FR-004): the signed-in user's saved language
// from /users/me (cache only), and the instance default from /auth/locale.
// The locale request is public and cached for the session; a failure leaves
// the instance default unset, so resolution falls through to English.
export function AppI18nProvider({ children }: { children: ReactNode }) {
  const userLanguage = useCachedUserLanguage();
  const locale = useQuery({
    queryKey: ["auth-locale"],
    // Loaded on first use: endpoints.ts builds its clients from @/lib/api at
    // import time, so a static import would pull that into every module that
    // only needs useLanguage (the public share page among them).
    queryFn: async () => (await import("@/lib/endpoints")).Auth.locale(),
    staleTime: Infinity,
    retry: false,
  });
  const instanceDefault = locale.data?.defaultLanguage;
  return (
    <I18nProvider userLanguage={userLanguage} instanceDefault={instanceDefault}>
      {children}
    </I18nProvider>
  );
}
