<script lang="ts">
  // The computer's summary line of the network quality test; it opens a
  // pop-up with the numbers and the curve.
  import type { NetQuality } from "../lib/nq.svelte";
  import { dismissable, tr } from "../lib/state.svelte";
  import NqBody from "./NqBody.svelte";
  import Reading from "./Reading.svelte";

  let { nq }: { nq: NetQuality } = $props();
  const t = $derived(tr());
  const f = $derived(nq.frame(t));
  let strip = $state<HTMLElement>();
  let row = $state<HTMLButtonElement>();

  function setOpen(on: boolean) {
    nq.open = on;
    if (!on) nq.hide();
  }
  // Without a result (never tested, or the last test failed) the line starts
  // a test; with one it opens and closes the pop-up.
  function click() {
    if (nq.phase !== "running" && !nq.hasResult) {
      nq.start();
      setOpen(true);
    } else setOpen(!nq.open);
  }
  $effect(() => dismissable(() => strip, () => nq.open, () => {
    if (strip?.contains(document.activeElement)) row?.focus();
    setOpen(false);
  }));
</script>

<div class="nq-strip" id="nq-strip" bind:this={strip}>
  <!-- While testing the divider doubles as a progress bar, seen with the pop-up closed too. -->
  <i class="nq-prog" aria-hidden="true" hidden={!f.running} style:width="{f.progress * 100}%"></i>
  <button type="button" class="nq-row" aria-expanded={nq.open} aria-haspopup="dialog" aria-controls="nq-pop" bind:this={row} onclick={click}>
    <span class="nq-row-l">
      <span class="sec-title">{t.nqTitle}</span>
      {#key f.tone}
        <span class="nq-st {f.phase === 'idle' ? 't-hint' : 't-' + f.tone}" role="status">
          {#if f.phase !== "idle"}<i class="nq-dot {f.tone}"></i>{/if}
          {#if f.running}<span class="shiny">{t.nqRunning(f.elapsed)}</span>{:else}<span>{f.phase === "idle" ? t.nqHint : f.status}</span>{/if}
        </span>
      {/key}
    </span>
    <span class="nq-row-r">
      {#if f.fresh}
        <span class="nq-go">{f.phase === "fail" ? t.nqAgain : t.nqStart}</span>
      {:else}
        <Reading value={f.med} unit="ms" />
        <span class="nq-chev"><svg aria-hidden="true" width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M4 10l4-4 4 4" /></svg></span>
      {/if}
    </span>
  </button>
  <div class="nq-pop" id="nq-pop" role="dialog" aria-label={t.nqTitle} hidden={!nq.open}><div class="nq-body"><NqBody {nq} mobile={false} /></div></div>
</div>
