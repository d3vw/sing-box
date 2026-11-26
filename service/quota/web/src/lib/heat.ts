import type { Dict } from "./i18n";
import type { Heat, HeatCell } from "./types";
import { slotRange } from "./state.svelte";

export function cellClass(c: HeatCell) {
  return c.f ? "future" : c.t ? "heat" : "empty";
}

export function cellStyle(c: HeatCell, slot: number) {
  return (c.t && !c.f ? `--t:${c.t};` : "") + `--c:${slot}`;
}

export function rowLabel(t: Dict, heat: Heat, r: number, short = false) {
  const row = heat.rows[r];
  return row.today ? (short ? t.todayShort : t.today) : (short ? t.wdShort : t.wd)[row.w];
}

export function cellTitle(t: Dict, heat: Heat, r: number, s: number) {
  return rowLabel(t, heat, r) + " · " + t.slot(s, slotRange(s));
}

export function cellValue(t: Dict, c: HeatCell) {
  return c.f ? t.notYet : c.v;
}

export function cellShare(t: Dict, c: HeatCell) {
  return c.f || !c.sh ? null : t.tipShare(c.sh);
}

export function inPeak(heat: Heat, s: number) {
  return !!heat.insight && s >= heat.insight.lo && s <= heat.insight.hi;
}

/** The legend's intensities; 0 is drawn as an empty cell. */
export const ramp = [0.25, 0.5, 0.75, 1];
