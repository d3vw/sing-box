// The page data the portal serves, as built by buildPage in ../../portal_page.go.

export type Tone = "ok" | "warn" | "err";

export interface PageUser {
  tag: string;
  name: string;
  blocked: boolean;
  remain: string;
  up: string;
  down: string;
  used: string;
  quota: string;
  usedPct: string;
  /** Where the ruler points, in percent of the quota. */
  p: number;
  /** The upload part of p. */
  pu: number;
  ticks: string[];
  tone: Tone;
  validUntil?: string;
  expired?: boolean;
  conns: number;
}

export interface Reset {
  m: number;
  d: number;
  left: number;
}

export interface Expiry {
  y: number;
  m: number;
  d: number;
  left: number;
  tone: Tone;
}

export type MonthDay = [number, number];

export interface Forecast {
  tone: Tone;
  /** Chance to run out before the end, 0–1. */
  p: number;
  left: string;
  range: string;
  perDay: string;
  end: "reset" | "expiry";
  from: MonthDay | null;
  to: MonthDay | null;
}

export interface Bar {
  v: string;
  m: number;
  d: number;
  w: number;
  ago: number;
  hot: boolean;
  over?: string;
  /** Height in percent of the chart. */
  h: number;
}

export interface Daily {
  bars: Bar[];
  days: number;
  avg: string;
  avgPos: number;
  peak: number;
  peakValue: string;
  today: string;
  budget: string;
  budgetPos: number;
  over: number;
  first: string;
  mid: string;
}

export interface HeatCell {
  v: string;
  sh?: string;
  t?: number;
  f?: boolean;
}

export interface HeatRow {
  w: number;
  today: boolean;
  cells: HeatCell[];
}

export interface Slot {
  s: number;
  cur?: boolean;
  pk?: boolean;
}

export interface Insight {
  lo: number;
  hi: number;
  range: string;
  share: number;
}

export interface Heat {
  rows: HeatRow[];
  slots: Slot[];
  insight: Insight | null;
}

export interface Subscription {
  uri: string;
  qr?: string;
}

export interface Profile {
  id: string;
  name: string;
  server: string;
  server_port: number;
  method: string;
  password: string;
  mux?: boolean;
  padding?: boolean;
}

export interface Profiles {
  active: string;
  profiles: Profile[];
}

export interface Outbound extends Profiles {
  methods: string[];
}

export interface PageData {
  title: string;
  self: PageUser | null;
  reset: Reset | null;
  expiry: Expiry | null;
  forecast: Forecast | null;
  daily: Daily | null;
  heat: Heat | null;
  vs: number | null;
  subscription: Subscription | null;
  outbound: Outbound | null;
  conns: { active: number; closed: number } | null;
  users: PageUser[] | null;
}

export interface ConnRecord {
  id: string;
  network: string;
  protocol?: string;
  host: string;
  dest: string;
  outbound?: string;
  rule?: string;
  upload: number;
  download: number;
  created_at: number;
  closed_at?: number;
  active: boolean;
}

export interface ConnSnapshot {
  now: number;
  active_count: number;
  closed_total: number;
  upload: number;
  download: number;
  active?: ConnRecord[];
  closed?: ConnRecord[];
}
