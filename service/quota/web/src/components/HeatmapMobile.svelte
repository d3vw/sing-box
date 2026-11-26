<script lang="ts">
  // Busy hours on a phone: twelve shichen down by seven days across; tapping
  // a cell reads it out above the grid.
  import type { Heat } from "../lib/types";
  import { clamp, tr } from "../lib/state.svelte";
  import { cellClass, cellShare, cellStyle, cellTitle, cellValue, inPeak, ramp, rowLabel } from "../lib/heat";
  import Rich from "./Rich.svelte";

  let { heat }: { heat: Heat } = $props();
  const t = $derived(tr());
  const nd = $derived(heat.rows.length);
  const nowSlot = $derived(Math.max(0, heat.slots.findIndex((s) => s.cur)));

  let pick = $state<{ d: number; h: number } | null>(null);
  const cur = $derived(pick ?? { d: nd - 1, h: nowSlot });
  let peakOn = $state(false);
  let cells = $state<HTMLElement>();

  function click(e: MouseEvent) {
    const r = cells!.getBoundingClientRect();
    pick = { d: clamp(Math.floor(((e.clientX - r.left) / r.width) * nd), 0, nd - 1), h: clamp(Math.floor(((e.clientY - r.top) / r.height) * 12), 0, 11) };
  }
  function keydown(e: KeyboardEvent) {
    let { d, h } = cur;
    switch (e.key) {
      case "ArrowLeft": d--; break;
      case "ArrowRight": d++; break;
      case "ArrowUp": h--; break;
      case "ArrowDown": h++; break;
      case "Home": h = 0; break;
      case "End": h = 11; break;
      default: return;
    }
    e.preventDefault();
    pick = { d: clamp(d, 0, nd - 1), h: clamp(h, 0, 11) };
  }
  const picked = $derived(heat.rows[cur.d].cells[cur.h]);
</script>

<section class="chart hm-m">
  <div class="sec-hd"><h2>{t.heatTitle}</h2><span class="legend"><span>{t.less}</span><i class="c empty"></i>{#each ramp as r (r)}<i class="c heat" style="--t:{r}"></i>{/each}<span>{t.more}</span></span></div>
  {#if heat.insight}
    <p class="peak-sub">
      <Rich parts={t.heatSub(heat.insight.range, heat.insight.share)}>
        {#snippet peak(text: string)}
          <button type="button" class="peak" aria-pressed={peakOn} onclick={() => (peakOn = !peakOn)}>{text}</button>
        {/snippet}
      </Rich>
    </p>
  {/if}
  <div class="pick" aria-live="polite"><span class="pick-l" id="pick-l">{cellTitle(t, heat, cur.d, cur.h)}</span><span class="pick-v"><b id="pick-v">{cellValue(t, picked)}</b><span id="pick-s">{cellShare(t, picked) || ""}</span></span></div>
  <div class="hmm-grid">
    <div class="hmm-hours">{#each heat.slots as slot, h (h)}<span class:cur={slot.cur} class:hl={h === cur.h}>{t.slotTick(h)}</span>{/each}</div>
    <div class="hmm-cells" id="hmm-cells" role="group" tabindex="0" bind:this={cells} aria-label={t.heatChart} onclick={click} onkeydown={keydown}>
      {#each heat.slots as _, h (h)}
        {#each heat.rows as row, d (d)}
          <div class="c {cellClass(row.cells[h])}" style={cellStyle(row.cells[h], h)} aria-hidden="true"
            class:sel={h === cur.h && d === cur.d} class:dim={peakOn && !inPeak(heat, h)}></div>
        {/each}
      {/each}
    </div>
    <span></span>{#each heat.rows as row, d (d)}<span class="hmm-day" class:today={row.today} class:hl={d === cur.d}>{rowLabel(t, heat, d, true)}</span>{/each}
  </div>
</section>
