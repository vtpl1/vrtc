import { useEffect, useState } from 'react';

type Theme = 'light' | 'dark' | 'system';

const STORAGE_KEY = 'vrtc-theme';

function getEffectiveTheme(theme: Theme): 'light' | 'dark' {
  if (theme !== 'system') return theme;
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export function useTheme() {
  const [theme, setThemeState] = useState<Theme>(() => {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === 'light' || stored === 'dark' || stored === 'system') return stored;
    return 'system';
  });

  const effective = getEffectiveTheme(theme);

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', effective);
  }, [effective]);

  function setTheme(next: Theme) {
    setThemeState(next);
    localStorage.setItem(STORAGE_KEY, next);
  }

  return { theme, effective, setTheme } as const;
}
