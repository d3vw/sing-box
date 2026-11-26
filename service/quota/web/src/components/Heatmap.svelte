<script lang="ts">
  // Busy hours on a computer: seven days by twelve shichen; hovering a cell
  // reads it out and highlights its day in the daily chart.
  import type { Heat } from "../lib/types";
  import type { ChartLink } from "../lib/link";
  import { clamp, tr } from "../lib/state.svelte";
  import { cellClass, cellShare, cellStyle, cellTitle, cellValue, inPeak, ramp, rowLabel } from "../lib/heat";
  import Rich from "./Rich.svelte";
  import Tip, { type TipContent } from "./Tip.svelte";

  let { heat, link }: { heat: Heat; link: ChartLink } = $props();
  const t = $derived(tr());
  const last = $derived(heat.rows.length - 1);

  let hover = $state<{ r: number; c: number } | null>(null);
  let peakOn = $state(false);
  const cellEls: HTMLElement[] = $state([]);
  const linked = $derived(link.heat !== null && link.heat <= last ? last - link.heat : null);

  function enter(r: number, c: number) {
    hover = { r, c };
    link.bars = last - r;
  }
  function leave() {
    hover = null;
    link.bars = null;
  }
  function keydown(e: KeyboardEvent) {
    const cur = hover || { r: last, c: 0 };
    const d = ({ ArrowUp: [-1, 0], ArrowDown: [1, 0], ArrowLeft: [0, -1], ArrowRight: [0, 1] } as Record<string, number[]>)[e.key];
    if (!d) return;
    e.preventDefault();
    enter(clamp(cur.r + d[0], 0, last), clamp(cur.c + d[1], 0, 11));
  }

  const tip = $derived.by((): TipContent | null => {
    if (!hover) return null;
    const c = heat.rows[hover.r].cells[hover.c];
    return { title: cellTitle(t, heat, hover.r, hover.c), value: cellValue(t, c), note: cellShare(t, c) };
  });
  // Cells at either end align their readout with the edge so it stays on
  // the card.
  const tipStyle = $derived.by(() => {
    if (!hover) return "";
    const e = cellEls[hover.r * 12 + hover.c];
    if (!e) return "";
    const align = hover.c < 2 ? "left" : hover.c > 9 ? "right" : "center";
    const x = align === "left" ? e.offsetLeft : align === "right" ? e.offsetLeft + e.offsetWidth : e.offsetLeft + e.offsetWidth / 2;
    return `right:auto;left:${x}px;top:${e.offsetTop - 8}px;transform:translate(${align === "left" ? "0" : align === "right" ? "-100%" : "-50%"},-100%)`;
  });
</script>

<section class="chart hm">
  <div class="sec-hd"><h2>{t.heatTitle}</h2><span class="legend"><span>{t.less}</span><i class="c empty"></i>{#each ramp as r (r)}<i class="c heat" style="--t:{r}"></i>{/each}<span>{t.more}</span></span></div>
  {#if heat.insight}
    <p class="peak-sub">
      <Rich parts={t.heatSub(heat.insight.range, heat.insight.share)}>
        {#snippet peak(text: string)}
          <span class="peak" tabindex="0" role="button" onpointerenter={() => (peakOn = true)} onpointerleave={() => (peakOn = false)} onfocus={() => (peakOn = true)} onblur={() => (peakOn = false)}>{text}</span>
        {/snippet}
      </Rich>
    </p>
  {/if}
  <div class="hm-grid" id="hm-grid" role="group" tabindex="0" class:peak-on={peakOn} aria-label={t.heatChart} onpointerleave={leave} onblur={leave} onkeydown={keydown}>
    {#each heat.rows as row, r (r)}
      <span class="hm-day" class:today={row.today} class:hl={hover?.r === r || linked === r}>{rowLabel(t, heat, r)}</span>
      {#each row.cells as cell, c (c)}
        <div class="c {cellClass(cell)}" style={cellStyle(cell, c)} bind:this={cellEls[r * 12 + c]}
          class:sel={hover?.r === r && hover?.c === c}
          class:dim={(peakOn && !inPeak(heat, c)) || (linked !== null && linked !== r)}
          onpointerenter={() => enter(r, c)}></div>
      {/each}
    {/each}
    <span></span>
    {#each heat.slots as slot, c (c)}<span class="hm-hr" class:cur={slot.cur} class:pk={slot.pk} class:hl={hover?.c === c}>{t.slotTick(c)}</span>{/each}
    <Tip id="hm-tip" {tip} style={tipStyle} />
  </div>
</section>
