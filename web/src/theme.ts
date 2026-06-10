// Framework-free dark/light/system theme state.
//
// No side effects at import time: every DOM / localStorage / matchMedia access
// happens inside a function so the module is safe to import under SSR or tests.
// Reads go through window.localStorage, window.matchMedia and
// document.documentElement so they can be stubbed in tests.

export type ThemeMode = "system" | "light" | "dark";

export const THEME_KEY = "fabrika-theme";

const ORDER: ThemeMode[] = ["system", "light", "dark"];

const GLYPH: Record<ThemeMode, string> = {
  system: "◐",
  light: "☀",
  dark: "☾",
};

const LABEL: Record<ThemeMode, string> = {
  system: "system",
  light: "light",
  dark: "dark",
};

function isMode(v: unknown): v is ThemeMode {
  return v === "system" || v === "light" || v === "dark";
}

export function getMode(): ThemeMode {
  const v = window.localStorage.getItem(THEME_KEY);
  return isMode(v) ? v : "system";
}

export function effectiveTheme(mode: ThemeMode): "light" | "dark" {
  if (mode === "system") {
    return window.matchMedia("(prefers-color-scheme: light)").matches
      ? "light"
      : "dark";
  }
  return mode;
}

function apply(mode: ThemeMode): void {
  const isLight = effectiveTheme(mode) === "light";
  document.documentElement.classList.toggle("light", isLight);
}

export function setMode(mode: ThemeMode): void {
  window.localStorage.setItem(THEME_KEY, mode);
  apply(mode);
}

export function cycleMode(): ThemeMode {
  const next = ORDER[(ORDER.indexOf(getMode()) + 1) % ORDER.length];
  setMode(next);
  return next;
}

export function initTheme(): void {
  apply(getMode());
}

export function themeToggle(): HTMLElement {
  const btn = document.createElement("button");
  btn.className = "theme-toggle";
  btn.type = "button";

  const paint = (mode: ThemeMode) => {
    btn.textContent = GLYPH[mode];
    btn.title = `Theme: ${LABEL[mode]}`;
  };

  paint(getMode());
  btn.addEventListener("click", () => {
    paint(cycleMode());
  });

  return btn;
}
