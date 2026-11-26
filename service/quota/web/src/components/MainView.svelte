<script lang="ts">
  import { onDestroy } from "svelte";
  import type { PageData, PageUser } from "../lib/types";
  import type { ChartLink } from "../lib/link";
  import { NetQuality } from "../lib/nq.svelte";
  import { tr } from "../lib/state.svelte";
  import Remain from "./Remain.svelte";
  import Daily from "./Daily.svelte";
  import Heatmap from "./Heatmap.svelte";
  import HeatmapMobile from "./HeatmapMobile.svelte";
  import NqBody from "./NqBody.svelte";
  import NqStrip from "./NqStrip.svelte";
  import Subscription from "./Subscription.svelte";
  import Updated from "./Updated.svelte";
  import Users from "./Users.svelte";

  let { data, self }: { data: PageData; self: PageUser } = $props();
  const t = $derived(tr());
  const link: ChartLink = $state({ bars: null, heat: null });
  // svelte-ignore state_referenced_locally
  const nq = new NetQuality(self.tag + "/" + self.name);
  onDestroy(() => nq.destroy());

  // On a phone the network quality card sits low on the page: its numbers
  // and curve move once it scrolls into view.
  let card = $state<HTMLElement>();
  $effect(() => {
    if (!card || nq.seen) return;
    const ob = new IntersectionObserver((es) => {
      if (es.some((x) => x.isIntersecting)) {
        nq.seen = true;
        ob.disconnect();
      }
    }, { threshold: 0.25 });
    ob.observe(card);
    return () => ob.disconnect();
  });
</script>

<div class="card" class:no-side={!data.subscription}>
  <section class="main">
    <Remain {data} {self} />
    <div class="ruler {self.tone}" style="--p:{self.p}%;--pu:{self.pu}%">
      <div class="rl-track">
        <i class="rl-minor"></i><i class="rl-major"></i><i class="rl-end"></i><i class="rl-base"></i><i class="rl-up"></i><i class="rl-down"></i><i class="rl-ptr"></i>
        <span class="rl-label" class:flip={self.p > 70} class:flip-m={self.p > 55}>{t.used(self.used, self.usedPct)}</span>
      </div>
      <div class="rl-ticks">{#each self.ticks as tick, i (i)}<span>{tick}</span>{/each}</div>
      <div class="ud">
        <span><i style="background:var(--up)"></i><span>{t.upload}</span><b class="strong">{self.up}</b></span>
        <span><i style="background:var(--down)"></i><span>{t.download}</span><b class="strong">{self.down}</b></span>
      </div>
    </div>
    {#if data.daily || data.heat}
      <div class="charts-wrap"><div class="charts">
        {#if data.daily}<Daily daily={data.daily} vs={data.vs} {link} />{/if}
        {#if data.heat}<Heatmap heat={data.heat} {link} /><HeatmapMobile heat={data.heat} />{/if}
      </div></div>
    {/if}
    <section class="chart nq-card" id="nq-card" bind:this={card}>
      <div class="nq-hd"><h2>{t.nqTitle}</h2><span class="nq-note">{t.nqNote}</span></div>
      <div class="nq-body"><NqBody {nq} mobile={true} /></div>
    </section>
    <Updated mobile />
  </section>
  {#if data.subscription}
    <section class="side">
      <Subscription sub={data.subscription} />
      <NqStrip {nq} />
    </section>
  {/if}
</div>
