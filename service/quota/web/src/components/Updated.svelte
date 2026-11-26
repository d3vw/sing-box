<script lang="ts">
  // When the data was last updated, or that updating failed.
  import { page, refresh, tr, ui } from "../lib/state.svelte";
  let { mobile = false }: { mobile?: boolean } = $props();
  const t = $derived(tr());
</script>

<svelte:element this={mobile ? "div" : "span"} class={mobile ? "upd upd-m" : "upd"}>
  {#if page.refreshing}
    <span class="shiny">{t.refreshing}</span> · {t.bjt}
  {:else if !page.stale}
    {t.updatedAgo(Math.max(0, Math.floor((ui.now - page.loadedAt) / 60000)))} · {t.bjt}
  {:else}
    <span class="err">{t.refreshFailed}</span><button type="button" onclick={refresh}>{t.retry}</button>
  {/if}
</svelte:element>
