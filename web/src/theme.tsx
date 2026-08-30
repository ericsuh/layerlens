import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";

/**
 * Theme handling (DESIGN §2.4).
 *
 * "system" is the default and needs no JavaScript at all: the stylesheet
 * defines the dark palette under `prefers-color-scheme: dark`, so the correct
 * colors are painted before the bundle runs. An explicit choice stamps
 * `data-theme` on <html>, which both the media-query block
 * (`:root:not([data-theme="light"])`) and the override block
 * (`:root[data-theme="dark"]`) respect.
 */
export type ThemeChoice = "system" | "light" | "dark";

const STORAGE_KEY = "layerlens.theme";

interface ThemeContextValue {
  choice: ThemeChoice;
  /** What is actually on screen right now — the choice, or the system answer. */
  resolved: "light" | "dark";
  setChoice: (choice: ThemeChoice) => void;
  toggle: () => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

function readStoredChoice(): ThemeChoice {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (stored === "light" || stored === "dark" || stored === "system") {
      return stored;
    }
  } catch {
    // Private mode or blocked storage: the system preference is a fine default.
  }
  return "system";
}

function systemPrefersDark(): boolean {
  return typeof window.matchMedia === "function"
    ? window.matchMedia("(prefers-color-scheme: dark)").matches
    : false;
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [choice, setChoiceState] = useState<ThemeChoice>(readStoredChoice);
  const [prefersDark, setPrefersDark] = useState(systemPrefersDark);

  useEffect(() => {
    if (typeof window.matchMedia !== "function") {
      return;
    }
    const query = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = (event: MediaQueryListEvent) => {
      setPrefersDark(event.matches);
    };
    query.addEventListener("change", onChange);
    return () => {
      query.removeEventListener("change", onChange);
    };
  }, []);

  useEffect(() => {
    const root = document.documentElement;
    if (choice === "system") {
      root.removeAttribute("data-theme");
    } else {
      root.setAttribute("data-theme", choice);
    }
    try {
      window.localStorage.setItem(STORAGE_KEY, choice);
    } catch {
      // Persisting is a convenience, never a requirement.
    }
  }, [choice]);

  const resolved: "light" | "dark" =
    choice === "system" ? (prefersDark ? "dark" : "light") : choice;

  const setChoice = useCallback((next: ThemeChoice) => {
    setChoiceState(next);
  }, []);

  const toggle = useCallback(() => {
    setChoiceState(resolved === "dark" ? "light" : "dark");
  }, [resolved]);

  const value = useMemo<ThemeContextValue>(
    () => ({ choice, resolved, setChoice, toggle }),
    [choice, resolved, setChoice, toggle],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const value = useContext(ThemeContext);
  if (value === null) {
    throw new Error("useTheme must be used inside a ThemeProvider");
  }
  return value;
}

export function ThemeToggle() {
  const { resolved, toggle } = useTheme();
  const target = resolved === "dark" ? "light" : "dark";
  return (
    <button
      type="button"
      className="ll-icon-btn"
      onClick={toggle}
      aria-label={`Switch to ${target} theme`}
      title={`Switch to ${target} theme`}
    >
      <span aria-hidden="true">{resolved === "dark" ? "☀" : "☾"}</span>
    </button>
  );
}
