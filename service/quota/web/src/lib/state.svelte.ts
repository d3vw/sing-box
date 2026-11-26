import { flushSync } from "svelte";
import { dicts, type Dict, type Lang } from "./i18n";
import type { PageData } from "./types";

export function load(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function store(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {}
}

const root = document.documentElement;

export const reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;

export type Theme = "light" | "dark";
export type View = "main" | "ob" | "cx";

// The head script in index.html has already set the theme and language on
// <html> before the first paint; the app takes over from there.
export const ui = $state({
  lang: (load("quota-language") === "en" ? "en" : "zh") as Lang,
  theme: (root.dataset.theme === "dark" ? "dark" : "light") as Theme,
  view: "main" as View,
  /** Ticks every 30 seconds for the "updated n min ago" lines. */
  now: Date.now(),
});

/** The dictionary for the language in use; read it inside markup or $derived. */
export function tr(): Dict {
  return dicts[ui.lang];
}

export function setLang(lang: Lang) {
  ui.lang = lang;
  store("quota-language", lang);
  root.lang = lang === "zh" ? "zh-CN" : "en";
}

/** Switches the theme, revealed as a circle growing from the button pressed. */
export function setTheme(theme: Theme, from: HTMLElement) {
  const apply = () => {
    root.dataset.theme = theme;
    store("quota-theme", theme);
    flushSync(() => {
      ui.theme = theme;
    });
  };
  if (!document.startViewTransition || reduced) {
    apply();
    return;
  }
  const r = from.getBoundingClientRect();
  const x = r.left + r.width / 2;
  const y = r.top + r.height / 2;
  const radius = Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y));
  document.startViewTransition(apply).ready.then(() => {
    root.animate(
      { clipPath: [`circle(0px at ${x}px ${y}px)`, `circle(${radius}px at ${x}px ${y}px)`] },
      { duration: 450, easing: "cubic-bezier(.4,0,.2,1)", pseudoElement: "::view-transition-new(root)" },
    );
  });
}

// The page data: inlined by the portal for the first paint, then refreshed
// every minute without reloading the page.
function inlined(): PageData | null {
  try {
    const data = JSON.parse(document.getElementById("qd")?.textContent || "null");
    return data && typeof data === "object" ? data : null;
  } catch {
    return null;
  }
}

export const page = $state({
  data: inlined(),
  loadedAt: Date.now(),
  refreshing: false,
  stale: false,
});

export async function refresh() {
  if (page.refreshing) return;
  page.refreshing = true;
  try {
    const res = await fetch("/quota/api/page", { cache: "no-store", headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error(res.statusText);
    page.data = await res.json();
    page.loadedAt = Date.now();
    page.stale = false;
  } catch {
    page.stale = true;
  } finally {
    page.refreshing = false;
  }
}

/** Posts a form the way the portal's JSON endpoints take it. */
export async function post(url: string, body: URLSearchParams | string): Promise<{ ok: boolean; j: any }> {
  const res = await fetch(url, {
    method: "POST",
    body,
    headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" },
  });
  try {
    return { ok: res.ok, j: await res.json() };
  } catch {
    return { ok: false, j: {} };
  }
}

export function clamp(v: number, lo: number, hi: number) {
  return Math.min(hi, Math.max(lo, v));
}

export function isMouse(e: PointerEvent) {
  return e.pointerType === "mouse";
}

const hh = (h: number) => (h < 10 ? "0" : "") + h + ":00";
export const slotStart = (s: number) => (s * 2 + 23) % 24;
export const slotRange = (s: number) => {
  const h = slotStart(s);
  return hh(h) + "–" + hh((h + 2) % 24);
};

/**
 * Calls close on a pointerdown outside box or on Escape while isOpen();
 * returns the cleanup for $effect.
 */
export function dismissable(box: () => HTMLElement | undefined, isOpen: () => boolean, close: () => void) {
  const down = (e: PointerEvent) => {
    const b = box();
    if (isOpen() && b && !b.contains(e.target as Node)) close();
  };
  const key = (e: KeyboardEvent) => {
    if (e.key === "Escape" && isOpen()) close();
  };
  document.addEventListener("pointerdown", down);
  document.addEventListener("keydown", key);
  return () => {
    document.removeEventListener("pointerdown", down);
    document.removeEventListener("keydown", key);
  };
}
