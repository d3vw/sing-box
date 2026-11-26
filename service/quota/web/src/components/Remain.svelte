<script lang="ts">
  import type { PageData, PageUser } from "../lib/types";
  import { dismissable, isMouse, tr } from "../lib/state.svelte";
  import { InfoIcon } from "../lib/icons";
  import Counter from "./Counter.svelte";
  import Rich from "./Rich.svelte";

  let { data, self }: { data: PageData; self: PageUser } = $props();
  const t = $derived(tr());
  const F = $derived(data.forecast);

  // The forecast tag says how it goes; the sentence it opens tells only what
  // the tag does not: the range left for on track, the days it likely runs
  // out on for may run out, the chance for running out.
  const detail = $derived(
    !F ? "" : F.tone === "ok" ? t.fcLeft(F.left, F.end)
      : F.tone === "warn" || !F.from || !F.to ? t.fcChance(F.p)
      : t.fcDate(t.dateRange(F.from, F.to)),
  );
  const basis = $derived(
    !F ? [] : F.tone === "ok" ? t.basisLeft(F.range, F.perDay, F.end)
      : F.tone === "warn" && F.from && F.to ? t.basisRunOutBy(t.dateRange(F.from, F.to), F.perDay, F.end)
      : t.basisRunOut(F.p, F.perDay, F.end),
  );

  // On a computer the sentence pops up on hover and stays pinned after a
  // click; on a phone a tap unfolds it under the reset date.
  let mobileOpen = $state(false);
  let hover = $state(false), pinned = $state(false);
  let timer = 0;
  let box = $state<HTMLElement>();
  const later = (on: boolean) => (e: PointerEvent) => {
    if (!isMouse(e)) return;
    clearTimeout(timer);
    timer = setTimeout(() => (hover = on), on ? 80 : 160);
  };
  $effect(() => dismissable(() => box, () => hover || pinned, () => { clearTimeout(timer); hover = pinned = false; }));
</script>

<div class="top">
  <div class="remain">
    <div class="remain-head">
      <span class="lbl">{t.remaining}</span>
      {#if F}
        <button type="button" class="fc-tag fc-m {F.tone}" aria-expanded={mobileOpen} aria-controls="fc-basis-m" title={t.basisToggle} onclick={() => (mobileOpen = !mobileOpen)}><i class="dot"></i><span class="fc-t">{t.tone[F.tone]}</span><span class="fc-x">{detail}</span>{@html InfoIcon}</button>
      {/if}
    </div>
    <div class="remain-v num"><span class="remain-n" data-ui-number><Counter value={parseFloat(self.remain)} decimals={2} /></span><span class="remain-u">GB</span></div>
    {#if data.reset}<span class="reset-line" id="reset-line">{t.resetsOn(data.reset.m, data.reset.d, data.reset.left)}</span>{/if}
    {#if data.expiry}<span class="reset-line expiry-line {data.expiry.tone}" id="expiry-line">{t.expiresOn(data.expiry.y, data.expiry.m, data.expiry.d, data.expiry.left)}</span>{/if}
    {#if F}<p class="fc-basis fc-basis-m" id="fc-basis-m" hidden={!mobileOpen}><Rich parts={basis} /></p>{/if}
  </div>
  {#if F}
    <div class="fc-d" id="fc-d" bind:this={box} onpointerenter={later(true)} onpointerleave={later(false)}>
      <button type="button" class="fc-tag {F.tone}" aria-expanded={hover || pinned} aria-controls="fc-basis-d" title={t.basisToggle}
        onclick={() => { if (pinned) { pinned = false; hover = false; } else pinned = true; }}><i class="dot"></i><span class="fc-t">{t.tone[F.tone]}</span><span class="fc-x">{detail}</span>{@html InfoIcon}</button>
      <p class="fc-basis fc-pop" id="fc-basis-d" hidden={!(hover || pinned)}><Rich parts={basis} /></p>
    </div>
  {/if}
</div>
