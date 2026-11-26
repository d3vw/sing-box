<script lang="ts">
  // The daily chart: hovering shows the bar nearest the pointer, a finger
  // drags across it, arrow keys step through the days.
  import type { Daily } from "../lib/types";
  import type { ChartLink } from "../lib/link";
  import { clamp, isMouse, tr } from "../lib/state.svelte";
  import Tip, { type TipContent } from "./Tip.svelte";

  let { daily, vs, link }: { daily: Daily; vs: number | null; link: ChartLink } = $props();
  const t = $derived(tr());
  const B = $derived(daily.bars);
  const n = $derived(B.length);

  type Stat = "avg" | "peak" | "today";
  let active = $state<number | null>(null);
  let avgOn = $state(false);
  let statOn = $state<Stat | null>(null);
  let kbd = false;
  let timer = 0;
  let box = $state<HTMLElement>();
  const barEls: HTMLElement[] = $state([]);

  function select(i: number | null) {
    clearTimeout(timer);
    active = i;
    link.heat = i === null ? null : B[i].ago;
  }
  function reset() {
    kbd = false;
    avgOn = false;
    statOn = null;
    select(null);
  }
  function resetLater() {
    clearTimeout(timer);
    timer = setTimeout(reset, 1600);
  }
  function pickAt(x: number) {
    const r = box!.getBoundingClientRect();
    if (!r.width) return;
    kbd = false;
    select(clamp(Math.floor(((x - r.left) / r.width) * n), 0, n - 1));
  }
  function keydown(e: KeyboardEvent) {
    const cur = active === null ? n - 1 : active;
    const next = ({ ArrowLeft: cur - 1, ArrowRight: cur + 1, Home: 0, End: n - 1 } as Record<string, number>)[e.key];
    if (next == null) return;
    e.preventDefault();
    kbd = true;
    avgOn = false;
    select(clamp(next, 0, n - 1));
  }

  // The average, peak and today figures light up their line or bar on hover
  // and focus.
  function show(kind: Stat) {
    statOn = kind;
    if (kind === "avg") {
      select(null);
      avgOn = true;
    } else {
      avgOn = false;
      select(kind === "peak" ? daily.peak : n - 1);
    }
  }
  function statHandlers(kind: Stat) {
    return {
      onpointerenter: (e: PointerEvent) => { if (isMouse(e)) show(kind); },
      onpointerleave: (e: PointerEvent) => { if (isMouse(e)) reset(); },
      onpointerup: (e: PointerEvent) => { if (!isMouse(e)) { show(kind); resetLater(); } },
      onfocus: (e: FocusEvent) => { if ((e.currentTarget as HTMLElement).matches(":focus-visible")) { show(kind); kbd = true; } },
      onblur: () => { if (kbd) reset(); },
    };
  }

  const tip = $derived.by((): TipContent | null => {
    if (active === null || !B[active]) return null;
    const b = B[active];
    return {
      title: b.ago === 0 ? t.tipToday : t.dayLabel(b.m, b.d, b.w),
      value: b.v,
      note: b.ago === 0 ? t.vsUsual(vs) : b.hot && b.over ? t.tipOver(b.over) : null,
      warn: b.hot,
    };
  });
  // Next to the bar; on the right part of the chart, left of it.
  const tipStyle = $derived.by(() => {
    if (active === null || !barEls[active] || !box) return "top:0";
    const e = barEls[active];
    const x = e.offsetLeft + e.offsetWidth / 2;
    return active > n * 0.6 ? `top:0;left:auto;right:${box.clientWidth - x + 10}px` : `top:0;right:auto;left:${x + 10}px`;
  });
</script>

<section class="chart daily">
  <div class="sec-hd"><h2>{t.dailyTitle}</h2><span class="sec-sub" id="daily-sub">{t.lastDays(daily.days)}</span></div>
  <div class="stats">
    <div class="stat" class:on={statOn === "avg"} tabindex="0" {...statHandlers("avg")}><span class="stat-l">{t.statAvg}</span><span class="stat-v"><b>{daily.avg}</b></span></div>
    <div class="stat" class:on={statOn === "peak"} tabindex="0" {...statHandlers("peak")}><span class="stat-l">{t.statPeak}</span><span class="stat-v"><b>{daily.peakValue}</b></span></div>
    <div class="stat" class:on={statOn === "today"} tabindex="0" {...statHandlers("today")}><span class="stat-l">{t.today}</span><span class="stat-v"><b>{daily.today}</b><span id="vs-today">{t.vsUsual(vs)}</span></span></div>
  </div>
  <div class="bars" id="bars" role="group" tabindex="0" bind:this={box} style="--n:{n}"
    class:tipping={active !== null} class:avg-on={avgOn}
    aria-label={t.dailyChart(daily.days, daily.over, !!daily.budget)}
    onpointermove={(e) => { if (isMouse(e) || e.buttons) pickAt(e.clientX); }}
    onpointerdown={(e) => { if (isMouse(e)) return; box!.setPointerCapture(e.pointerId); pickAt(e.clientX); }}
    onpointerup={(e) => { if (!isMouse(e)) resetLater(); }}
    onpointercancel={reset}
    onpointerleave={(e) => { if (isMouse(e)) reset(); }}
    onkeydown={keydown}
    onblur={() => { if (kbd) reset(); }}>
    {#each B as b, i (i)}
      <div class="bar" class:today={b.ago === 0} class:hot={b.hot} class:on={i === active} class:dim={link.bars !== null && b.ago !== link.bars} bind:this={barEls[i]}><i style="height:{b.h}%;--i:{i}"></i></div>
    {/each}
    {#if daily.budget}<div class="bl budget" style="bottom:{daily.budgetPos}%" aria-hidden="true"><span><span>{t.budgetLabel}</span> <b>{daily.budget}</b></span></div>{/if}
    <div class="bl avg" style="bottom:{daily.avgPos}%" aria-hidden="true"><span><span>{t.statAvg}</span> <b>{daily.avg}</b></span></div>
    <Tip id="bars-tip" {tip} style={tipStyle} />
  </div>
  <div class="axis"><span class="num">{daily.first}</span><span class="num">{daily.mid}</span><span>{t.today}</span></div>
</section>
