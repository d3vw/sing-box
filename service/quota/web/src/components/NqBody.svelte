<script lang="ts">
  // The numbers, curve and button of the network quality test, as the
  // phone card (mobile) or inside the computer's pop-up.
  import type { NetQuality } from "../lib/nq.svelte";
  import { isMouse, tr } from "../lib/state.svelte";
  import Reading from "./Reading.svelte";
  import Tip from "./Tip.svelte";

  let { nq, mobile }: { nq: NetQuality; mobile: boolean } = $props();
  const t = $derived(tr());
  const f = $derived(nq.frame(t));
  const play = $derived(mobile ? nq.seen : true);
  // The curve appears once it can play and stays while there are packets.
  let drawn = $state(false);
  $effect(() => {
    if (!f.hasPlot) drawn = false;
    else if (play) drawn = true;
  });
  const gid = $derived("nq-fade-" + (mobile ? "m" : "d"));
  let plot = $state<HTMLElement>();
  let touchTimer = 0;

  const tipStyle = $derived.by(() => {
    if (!f.cur) return "";
    const pct = f.cur.pct;
    if (pct > 75) return `left:auto;right:calc(${100 - pct}% - 12px)`;
    if (pct < 25) return `right:auto;left:calc(${pct}% - 12px)`;
    return `right:auto;left:${pct}%;translate:-50% 0`;
  });
</script>

<div class="nq-stats">
  {#each f.stats as s, i (i)}
    <div class="nq-stat">
      <span>{s.l}</span>
      <span class="nq-line"><Reading value={s.v} unit={s.u} digits={s.dg} {play} /><small hidden={!s.n}>{s.n}</small></span>
    </div>
  {/each}
</div>
<div class="nq-plot" class:has={f.hasPlot} role="group" tabindex={f.hasPlot ? 0 : -1} aria-label={t.nqChart} bind:this={plot}
  onpointermove={(e) => { if (isMouse(e) || e.buttons) { clearTimeout(touchTimer); nq.pickAt(plot!, e.clientX); } }}
  onpointerdown={(e) => { if (isMouse(e)) return; plot!.setPointerCapture(e.pointerId); clearTimeout(touchTimer); nq.pickAt(plot!, e.clientX); }}
  onpointerup={(e) => { if (isMouse(e)) return; clearTimeout(touchTimer); touchTimer = setTimeout(() => nq.hide(), 1600); }}
  onpointercancel={() => nq.hide()}
  onpointerleave={(e) => { if (isMouse(e)) nq.hide(); }}
  onkeydown={(e) => { if (nq.step(e.key)) e.preventDefault(); }}
  onblur={() => { if (nq.kbd) nq.hide(); }}>
  {#if drawn}
    <svg viewBox="0 0 200 48" preserveAspectRatio="none" aria-hidden="true">
      <defs><linearGradient id={gid} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="currentColor" stop-opacity=".2" /><stop offset="1" stop-color="currentColor" stop-opacity="0" /></linearGradient></defs>
      <path class="nq-area" fill="url(#{gid})" d={f.area || "M0 0"} />
      <line class="nq-med" x1="0" x2="200" vector-effect="non-scaling-stroke" stroke-width="1" y1={f.medY ?? 0} y2={f.medY ?? 0} style:display={f.medY === null ? "none" : null} />
      <g>{#each f.stalls as x, i (i)}<line class="nq-stall" x1={x} x2={x} y1="2" y2="46" vector-effect="non-scaling-stroke" stroke-width="2" />{/each}</g>
      <path fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" vector-effect="non-scaling-stroke" d={f.line || "M0 0"} />
    </svg>
  {/if}
  <span class="nq-empty" hidden={!!f.line}></span>
  {#if mobile}<Tip tip={f.tip ? { ...f.tip, warn: f.tipBad } : null} style={tipStyle} />{/if}
  <!-- While testing the head follows the latest packet; looking at one shows a rule and a dot. -->
  {#if f.head && !f.cur && play}<span class="nq-head" style="left:{f.head.x}%;top:{f.head.y}%"><i></i><i></i></span>{/if}
  {#if f.cur}
    <span class="nq-cur" style="left:{f.cur.pct}%"></span>
    {#if f.cur.y !== null}<span class="nq-pt" style="left:{f.cur.pct}%;top:{f.cur.y}%"></span>{/if}
  {/if}
</div>
<div class="nq-foot">
  {#if mobile}
    <span class="nq-st t-{f.tone}" role="status">{#if !f.running}<i class="nq-dot {f.tone}"></i><span>{f.status}</span>{/if}</span>
  {:else}
    <!-- The pop-up is too small for a readout on the curve; it reads out here instead. -->
    <p role="status">
      {#if f.tip}<span>{f.tip.title}</span><span class={f.tipBad ? "warn" : null}><b>{f.tip.value}</b>{f.tip.note || ""}</span>
      {:else}<span style="white-space:normal">{t.nqNote}</span>{/if}
    </p>
  {/if}
  <button type="button" class="btn-sec nq-btn" disabled={f.running} onclick={() => nq.start()}><span class="nq-fill" hidden={!f.running} style:width="{f.progress * 100}%"></span><span>{f.running ? t.nqRunning(f.elapsed) : f.phase === "idle" ? t.nqStart : t.nqAgain}</span></button>
</div>
