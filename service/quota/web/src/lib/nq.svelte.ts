// Network quality: a small packet through the tunnel every 100 ms for 20
// seconds, echoed by the portal over /probe; it reports latency, jitter,
// the share of stalls and the latency curve per packet. On a computer it is
// a summary line at the foot of the side column that opens upward into the
// numbers and the curve; on a phone (or without a side column) it is a card
// of its own. Results are kept on this device for 12 hours.

import type { Dict } from "./i18n";

const DUR = 20000;
const GAP = 100;
const DRAIN = 3000;
const KEEP = 12 * 3600000;

export type Grade = "good" | "fair" | "poor";
type Phase = "idle" | "running" | "done" | "fail";

/** A packet sent t ms into the test; r is its round trip, null until back. */
interface Sample {
  t: number;
  r: number | null;
}

interface Summary {
  med: number;
  p95: number;
  jit: number;
  slow: number;
  cut: number;
  grade: Grade;
}

function summarize(S: Sample[]): Summary {
  const got: number[] = [];
  S.forEach((x) => { if (x.r !== null) got.push(x.r); });
  const sorted = got.slice().sort((a, b) => a - b);
  const q = (p: number) => (sorted.length ? sorted[Math.min(sorted.length - 1, Math.floor(p * sorted.length))] : 0);
  const med = q(0.5);
  let jit = 0;
  for (let i = 1; i < got.length; i++) jit += Math.abs(got[i] - got[i - 1]);
  jit = got.length > 1 ? jit / (got.length - 1) : 0;
  // A stall is twice as slow as usual and at least 100 ms slower.
  const cut = Math.max(med * 2, med + 100);
  const bad = S.length - got.length + got.filter((v) => v > cut).length;
  const slow = S.length ? (bad / S.length) * 100 : 0;
  return { med, p95: q(0.95), jit, slow, cut, grade: slow < 1 && jit < 15 ? "good" : slow >= 5 || jit >= 50 ? "poor" : "fair" };
}

export function fmtMs(v: number) {
  return (v < 10 ? v.toFixed(1) : Math.round(v)) + " ms";
}

export interface NqTip {
  title: string;
  value: string;
  note: string | null;
}

export interface NqStat {
  l: string;
  v: number | null;
  u: string;
  n: string;
  dg?: number;
}

/** Everything both views draw, from one state. */
export interface NqFrame {
  running: boolean;
  progress: number;
  elapsed: number;
  tone: string;
  status: string;
  stats: NqStat[];
  /** The latency in the summary line. */
  med: number | null;
  hasPlot: boolean;
  line: string;
  area: string;
  medY: number | null;
  stalls: number[];
  head: { x: number; y: number } | null;
  cur: { pct: number; y: number | null } | null;
  tip: NqTip | null;
  tipBad: boolean;
  phase: Phase;
  fresh: boolean;
}

export class NetQuality {
  #key: string;
  #S: Sample[] = [];
  #res: Summary | null = null;
  #testedAt = 0;
  #began = 0;
  #ticker = 0;
  /** Bumped whenever the test moves on; frames read it to redraw. */
  #version = $state(0);
  phase = $state<Phase>("idle");
  active = $state<number | null>(null);
  open = $state(false);
  /** The phone card's numbers and curve wait until it scrolls into view. */
  seen = $state(typeof IntersectionObserver === "undefined");
  kbd = false;

  constructor(key: string) {
    this.#key = "quota-nq:" + key;
    try {
      const saved = JSON.parse(localStorage.getItem(this.#key) || "null");
      if (saved && Date.now() - saved.at < KEEP && saved.s.length) {
        this.#S = saved.s.map((x: [number, number | null]) => ({ t: x[0], r: x[1] }));
        this.#res = summarize(this.#S);
        this.#testedAt = saved.at;
        this.phase = "done";
      }
    } catch {}
    this.#tick();
  }

  get count() {
    this.#version;
    return this.#S.length;
  }

  /** Whether there is a result to show, as opposed to a test to start. */
  get hasResult() {
    this.#version;
    return this.#res !== null;
  }

  #redraw() {
    this.#version++;
  }

  // While testing, redraw every packet (100 ms); otherwise every 30 seconds
  // for "n minutes ago".
  #tick() {
    clearInterval(this.#ticker);
    this.#ticker = setInterval(() => this.#redraw(), this.phase === "running" ? GAP : 30000);
  }

  destroy() {
    clearInterval(this.#ticker);
  }

  /** Picks the packet nearest x across plot. */
  pickAt(plot: HTMLElement, x: number) {
    const r = plot.getBoundingClientRect();
    if (!this.#S.length || !r.width) return;
    const at = ((x - r.left) / r.width) * DUR;
    let best = 0, bd = Infinity;
    this.#S.forEach((s, i) => {
      const d = Math.abs(s.t - at);
      if (d < bd) { bd = d; best = i; }
    });
    this.kbd = false;
    this.active = best;
  }

  step(key: string) {
    const n = this.#S.length;
    if (!n) return false;
    const c = this.active == null ? n - 1 : this.active;
    const next = ({ ArrowLeft: c - 1, ArrowRight: c + 1, Home: 0, End: n - 1 } as Record<string, number>)[key];
    if (next == null) return false;
    this.kbd = true;
    this.active = Math.min(n - 1, Math.max(0, next));
    return true;
  }

  hide() {
    this.kbd = false;
    this.active = null;
  }

  start() {
    if (this.phase === "running") return;
    this.#S = [];
    this.#began = 0;
    this.#res = null;
    this.active = null;
    this.phase = "running";
    const S = this.#S;
    let ws: WebSocket | undefined;
    const sent: number[] = [], back: boolean[] = [];
    let pending = 0, stopped = false, done = false, timer = 0;
    const end = (ok: boolean) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      if (ws) {
        ws.onopen = ws.onclose = ws.onerror = ws.onmessage = null;
        try { ws.close(); } catch {}
      }
      if (ok && S.some((x) => x && x.r !== null)) {
        this.#testedAt = Date.now();
        this.#res = summarize(S);
        this.phase = "done";
        try {
          localStorage.setItem(this.#key, JSON.stringify({ at: this.#testedAt, s: S.map((x) => [Math.round(x.t), x.r === null ? null : Math.round(x.r * 10) / 10]) }));
        } catch {}
      } else this.phase = "fail";
      this.#tick();
      this.#redraw();
    };
    const next = () => {
      const now = performance.now();
      if (now - this.#began > DUR - GAP / 2) {
        stopped = true;
        if (!pending) end(true);
        else timer = setTimeout(() => end(true), DRAIN);
        return;
      }
      const i = sent.length;
      sent.push(now);
      pending++;
      try { ws!.send(String(i)); } catch {}
      S[i] = { t: now - this.#began, r: null };
      // Each packet is due at its slot from the start, so delays don't add up.
      let due = this.#began + (i + 1) * GAP;
      if (due < now) due = now + GAP;
      timer = setTimeout(next, due - performance.now());
    };
    try {
      ws = new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/probe");
    } catch {
      setTimeout(() => end(false));
      return;
    }
    ws.onopen = () => {
      this.#began = performance.now();
      next();
      this.#redraw();
    };
    ws.onmessage = (e) => {
      const i = Number(e.data);
      if (sent[i] === undefined || back[i]) return;
      back[i] = true;
      pending--;
      S[i].r = performance.now() - sent[i];
      if (stopped && !pending) end(true);
    };
    ws.onerror = ws.onclose = () => end(sent.length >= 10);
    this.#tick();
    this.#redraw();
  }

  frame(t: Dict): NqFrame {
    this.#version;
    const S = this.#S, res = this.#res, phase = this.phase, active = this.active;
    const running = phase === "running";
    const since = running && this.#began ? performance.now() - this.#began : 0;
    const progress = Math.min(1, since / DUR);
    // While testing, a packet just sent isn't lost yet; it is after a second.
    const seenS = running ? S.filter((x) => x && (x.r !== null || since - x.t > 1000)) : S;
    const live = seenS.length ? summarize(seenS) : null;
    const now = res || (running && live && seenS.some((x) => x.r !== null) ? live : null);
    const cap = live ? Math.max(live.p95 * 1.3, live.med * 2, 10) : 10;
    const yOf = (v: number) => 46 - (Math.min(v, cap) / cap) * 44;
    const xOf = (ms: number) => (ms / DUR) * 200;
    // The curve breaks where packets lie too far apart (lost in between);
    // the area under it closes each piece down to the bottom on its own.
    let d = "", area = "", segFrom = "", segTo = "";
    let prev: number | null = null;
    let head: Sample | null = null;
    const stalls: number[] = [];
    const close = () => {
      if (segFrom) area += "L" + segTo + " 48L" + segFrom + " 48Z";
      segFrom = "";
    };
    S.forEach((x) => {
      if (!x) return;
      if (x.r === null) {
        prev = null;
        close();
        if (!running || since - x.t > 1000) stalls.push(xOf(x.t));
        return;
      }
      const joined = prev !== null && x.t - prev <= GAP * 3;
      const X = xOf(x.t).toFixed(1), pt = X + " " + yOf(x.r).toFixed(1);
      if (!joined) { close(); segFrom = X; }
      d += (joined ? "L" : "M") + pt;
      area += (joined ? "L" : "M") + pt;
      segTo = X;
      prev = x.t;
      head = x;
      if (live && x.r > live.cut) stalls.push(xOf(x.t));
    });
    close();
    const cur = active != null ? S[active] : null;
    const pct = cur ? (cur.t / DUR) * 100 : 0;
    const lost = !!cur && cur.r === null, waiting = lost && running;
    const tipBad = !!cur && !waiting && (lost || (live !== null && cur.r! > live.cut));
    const tip: NqTip | null = cur
      ? {
          title: t.nqTipAt((cur.t / 1000).toFixed(1), active! + 1),
          value: lost ? (waiting ? t.nqWaiting : t.nqLost) : fmtMs(cur.r!),
          note: !tipBad ? null : lost ? t.nqLostNote : t.nqSlowNote(fmtMs(cur.r! - (live ? live.med : 0))),
        }
      : null;
    const elapsed = running && this.#began ? Math.min(DUR / 1000, Math.floor(since / 1000)) : 0;
    const tone = running ? "running" : res ? res.grade : phase === "fail" ? "fail" : "idle";
    const status = running ? "" : res ? t.nqGrade[res.grade] + " · " + t.nqAgo(Math.floor((Date.now() - this.#testedAt) / 60000)) : phase === "fail" ? t.nqFail : t.nqIdle;
    const blank = running ? 0 : null;
    const h = head as Sample | null;
    return {
      running, progress, elapsed, tone, status, phase,
      stats: [
        { l: t.nqLatency, v: now ? now.med : blank, u: "ms", n: now ? t.nqP95(fmtMs(now.p95)) : "" },
        { l: t.nqJitter, v: now ? now.jit : blank, u: "ms", n: "" },
        { l: t.nqSlow, v: now ? now.slow : blank, u: "%", n: "", dg: !now || now.slow < 0.1 ? 0 : 1 },
      ],
      med: now ? now.med : blank,
      hasPlot: S.length > 0,
      line: d,
      area,
      medY: live ? yOf(live.med) : null,
      stalls,
      head: running && h && h.r !== null ? { x: (h.t / DUR) * 100, y: (yOf(h.r) / 48) * 100 } : null,
      cur: cur ? { pct, y: lost ? null : (yOf(cur.r!) / 48) * 100 } : null,
      tip,
      tipBad,
      fresh: !running && !res,
    };
  }
}
