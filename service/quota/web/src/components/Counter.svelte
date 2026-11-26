<script lang="ts">
  // A number whose digits roll like an odometer: when the value changes,
  // only the digits that changed roll to their new numeral, on a spring,
  // blurred vertically while fast. It starts at its value without rolling;
  // with reduced motion it jumps. Size, weight and color follow the parent.
  // Ported from the design's Counter.tsx (itself adapted from react-bits).
  import { onDestroy, onMount } from "svelte";

  let { value, decimals = 0 }: { value: number; decimals?: number } = $props();

  interface Slot {
    key: string;
    kind: "digit" | "symbol";
    digit: number;
    text: string;
    /** Left to right: higher integer places lower, decimals rising. */
    rank: number;
  }
  interface Item {
    kind: "digit" | "symbol";
    rank: number;
    pos: number;
    vel: number;
    target: number;
    /** How much of the slot shows (0–1) while digits come and go. */
    shown: number;
    shownVel: number;
    shownTarget: number;
    wait: number;
    exiting: boolean;
    natural: number;
    el: HTMLElement;
    numerals: HTMLElement[];
    strip: HTMLElement | null;
    blur: SVGFEGaussianBlurElement | null;
    filter: string;
    blurred: boolean;
  }

  /** About how long a digit takes to roll into place, in seconds. */
  const DURATION = 0.7;
  /** The bounce on arrival; 0 is none. */
  const BOUNCE = 0.12;
  /** How far apart neighboring digits start, lowest first, in seconds. */
  const STAGGER = 0.04;
  const TAU = Math.PI * 2;
  const SVG = "http://www.w3.org/2000/svg";
  let uid = 0;
  const id = `ctr${Math.random().toString(36).slice(2, 8)}`;

  const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));
  const mod = (v: number, base: number) => ((v % base) + base) % base;
  /** Numeral n's offset from the current position, folded into -5…5 so 9 and 0 sit together. */
  const wrap = (offset: number) => mod(offset + 5, 10) - 5;
  const prefersReduced = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

  /** Steps from one numeral to another: up as the value grows, down as it shrinks. */
  function stepBetween(from: number, to: number, direction: "up" | "down" | "shortest") {
    const up = mod(to - from, 10);
    if (direction === "up") return up;
    if (direction === "down") return up === 0 ? 0 : up - 10;
    return up > 5 ? up - 10 : up;
  }

  function slotsFor(v: number): Slot[] {
    const [int, frac = ""] = Math.abs(v).toFixed(decimals).split(".");
    const slots: Slot[] = [];
    if (v < 0) slots.push({ key: "minus", kind: "symbol", digit: 0, text: "−", rank: -10000 });
    [...int].forEach((char, i) => {
      const place = int.length - 1 - i;
      slots.push({ key: `i${place}`, kind: "digit", digit: Number(char), text: "", rank: -2 * place });
    });
    if (frac) slots.push({ key: "d", kind: "symbol", digit: 0, text: ".", rank: 0.5 });
    [...frac].forEach((char, i) => slots.push({ key: `f${i}`, kind: "digit", digit: Number(char), text: "", rank: 1 + i }));
    return slots;
  }

  const label = $derived(value.toFixed(decimals));
  let row = $state<HTMLElement>();
  let root = $state<HTMLElement>();
  const items = new Map<string, Item>();
  let raf = 0, last = 0, em = 16, current = 0, ready = false;

  function span(cls: string, text?: string) {
    const e = document.createElement("span");
    e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }

  function makeItem(slot: Slot, pos: number, shown: number): Item {
    let el: HTMLElement, numerals: HTMLElement[] = [], strip: HTMLElement | null = null, blur: SVGFEGaussianBlurElement | null = null, filter = "";
    if (slot.kind === "digit") {
      el = span("ctr-d");
      // An invisible numeral sets the width and baseline; the strip of 0–9 is what shows.
      el.appendChild(span("ctr-ph", String(slot.digit)));
      const win = span("ctr-win");
      strip = span("ctr-strip");
      for (let n = 0; n < 10; n++) {
        const numeral = span("ctr-n", String(n));
        numeral.style.transform = `translateY(${wrap(n - pos) * 100}%)`;
        strip.appendChild(numeral);
        numerals.push(numeral);
      }
      win.appendChild(strip);
      el.appendChild(win);
      const svg = document.createElementNS(SVG, "svg");
      svg.setAttribute("class", "ctr-svg");
      svg.setAttribute("aria-hidden", "true");
      const f = document.createElementNS(SVG, "filter");
      filter = `${id}-${uid++}`;
      f.setAttribute("id", filter);
      f.setAttribute("x", "-10%");
      f.setAttribute("y", "-50%");
      f.setAttribute("width", "120%");
      f.setAttribute("height", "200%");
      blur = document.createElementNS(SVG, "feGaussianBlur");
      blur.setAttribute("stdDeviation", "0 0");
      f.appendChild(blur);
      svg.appendChild(f);
      el.appendChild(svg);
    } else {
      el = span("ctr-s");
      el.appendChild(span("", slot.text));
    }
    return {
      kind: slot.kind, rank: slot.rank, pos, vel: 0, target: pos, shown, shownVel: 0, shownTarget: shown,
      wait: 0, exiting: false, natural: 0, el, numerals, strip, blur, filter, blurred: false,
    };
  }

  function paint(item: Item) {
    const shown = clamp(item.shown, 0, 1);
    if (item.kind === "digit") {
      item.numerals.forEach((numeral, n) => {
        numeral.style.transform = `translateY(${(wrap(n - item.pos) * 100).toFixed(3)}%)`;
      });
      if (item.blur && item.strip) {
        const radius = clamp((Math.abs(item.vel) - 3) * 0.006, 0, 0.09) * em;
        if (radius > 0.25) {
          item.blur.setAttribute("stdDeviation", `0 ${radius.toFixed(2)}`);
          if (!item.blurred) {
            item.strip.style.filter = `url(#${item.filter})`;
            item.blurred = true;
          }
        } else if (item.blurred) {
          item.strip.style.filter = "";
          item.blurred = false;
        }
      }
    }
    item.el.style.width = shown >= 1 ? "" : `${(item.natural * shown).toFixed(2)}px`;
    item.el.style.setProperty("--counter-shown", shown >= 1 ? "" : shown.toFixed(3));
  }

  function frame(now: number) {
    raf = 0;
    const elapsed = clamp((now - last) / 1000, 0, 1 / 20);
    last = now;
    const reduced = prefersReduced();
    const omega = TAU / DURATION, zeta = 1 - BOUNCE, presence = TAU / (DURATION * 0.85);
    let busy = false;
    for (const [key, item] of items) {
      if (reduced) {
        item.wait = 0;
        item.pos = item.target;
        item.vel = 0;
        item.shown = item.shownTarget;
        item.shownVel = 0;
      } else {
        let left = elapsed;
        if (item.wait > 0) {
          const used = Math.min(item.wait, left);
          item.wait -= used;
          left -= used;
        }
        if (left > 0) {
          // A spring integrated in small steps, steady through dropped frames.
          const steps = Math.ceil(left * 240), dt = left / steps;
          for (let i = 0; i < steps; i++) {
            item.vel += (-omega * omega * (item.pos - item.target) - 2 * zeta * omega * item.vel) * dt;
            item.pos += item.vel * dt;
            item.shownVel += (-presence * presence * (item.shown - item.shownTarget) - 2 * presence * item.shownVel) * dt;
            item.shown += item.shownVel * dt;
          }
        }
      }
      const settled = item.wait <= 0 && Math.abs(item.pos - item.target) < 0.002 && Math.abs(item.vel) < 0.02
        && Math.abs(item.shown - item.shownTarget) < 0.002 && Math.abs(item.shownVel) < 0.02;
      if (settled) {
        item.target = mod(Math.round(item.target), 10);
        item.pos = item.target;
        item.vel = 0;
        item.shown = item.shownTarget;
        item.shownVel = 0;
        if (item.exiting) {
          item.el.remove();
          items.delete(key);
          continue;
        }
      } else busy = true;
      paint(item);
    }
    if (busy) raf = requestAnimationFrame(frame);
  }

  /** Puts the slots' elements in rank order. */
  function arrange() {
    const sorted = [...items.values()].sort((a, b) => a.rank - b.rank);
    sorted.forEach((item, i) => {
      if (row!.children[i] !== item.el) row!.insertBefore(item.el, row!.children[i] || null);
    });
  }

  function show(v: number) {
    const slots = slotsFor(v);
    if (!ready) {
      slots.forEach((slot) => items.set(slot.key, makeItem(slot, slot.kind === "digit" ? slot.digit : 0, 1)));
      arrange();
      ready = true;
      current = v;
      return;
    }
    const previous = current;
    current = v;
    const trend = v > previous ? "up" : v < previous ? "down" : "shortest";
    const next = new Set(slots.map((s) => s.key));
    const moving: Item[] = [];
    // New places grow in from zero width; places gone shrink away.
    for (const slot of slots) {
      let item = items.get(slot.key);
      if (!item) {
        item = makeItem(slot, 0, 0);
        items.set(slot.key, item);
      }
      item.exiting = false;
      item.shownTarget = 1;
      if (slot.kind === "digit") {
        (item.el.firstChild as HTMLElement).textContent = String(slot.digit);
        const step = stepBetween(mod(Math.round(item.target), 10), slot.digit, trend);
        if (step !== 0 || item.shown < 1) {
          item.target += step;
          moving.push(item);
        }
      }
    }
    for (const [key, item] of items) {
      if (next.has(key) || item.exiting) continue;
      item.exiting = true;
      item.shownTarget = 0;
      if (item.kind !== "digit") continue;
      item.target += stepBetween(mod(Math.round(item.target), 10), 0, trend);
      moving.push(item);
    }
    moving.sort((a, b) => b.rank - a.rank).forEach((item, order) => (item.wait = order * STAGGER));
    arrange();
    em = parseFloat(getComputedStyle(root!).fontSize) || 16;
    for (const item of items.values()) {
      if (item.shown < 1 || item.shownTarget < 1) {
        item.el.style.width = "";
        item.natural = item.el.getBoundingClientRect().width;
      }
      paint(item);
    }
    if (!raf) {
      last = performance.now();
      raf = requestAnimationFrame(frame);
    }
  }

  onMount(() => show(Number(label)));
  $effect(() => {
    const v = Number(label);
    if (ready && v !== current) show(v);
  });
  onDestroy(() => cancelAnimationFrame(raf));
</script>

<span class="ctr" bind:this={root}><span class="ctr-sr">{label}</span><span class="ctr-row" aria-hidden="true" bind:this={row}></span></span>
