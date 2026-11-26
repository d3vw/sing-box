<script lang="ts">
  import type { PageUser } from "../lib/types";
  import { dismissable, setLang, setTheme, tr, ui, type Theme } from "../lib/state.svelte";
  import type { Lang } from "../lib/i18n";
  import Seg from "./Seg.svelte";
  import Updated from "./Updated.svelte";

  let { self }: { self: PageUser | null } = $props();
  const t = $derived(tr());

  // On phones the theme and language sit behind the "⋯" menu.
  let open = $state(false);
  let box = $state<HTMLElement>();
  $effect(() => dismissable(() => box, () => open, () => (open = false)));
</script>

<header class="hd">
  <div class="hd-l">
    <h1 class="hd-title">{t.title}</h1>
    {#if self}
      <div class="hd-meta">
        <span class="hd-div" aria-hidden="true"></span>
        <span class="hd-node">{self.name}</span>
        <span class="tag">{self.tag}</span>
        <span class="status {self.expired || self.blocked ? 'bad' : 'ok'}"><i></i><span>{self.expired ? t.expired : self.blocked ? t.blocked : t.healthy}</span></span>
      </div>
    {/if}
  </div>
  <div class="hd-r" bind:this={box}>
    <Updated />
    <div class="ctl" class:open id="ctl" role="group">
      <div class="ctl-row">
        <span class="ctl-l">{t.themeLabel}</span>
        <Seg label={t.themeLabel} value={ui.theme} options={[{ v: "light", label: t.light }, { v: "dark", label: t.dark }]}
          onpick={(v: Theme, b) => { if (v !== ui.theme) setTheme(v, b); }} />
      </div>
      <div class="ctl-row">
        <span class="ctl-l">{t.langLabel}</span>
        <Seg label={t.langLabel} value={ui.lang} options={[{ v: "zh", label: "中文" }, { v: "en", label: "EN" }]}
          onpick={(v: Lang) => { if (v !== ui.lang) setLang(v); }} />
      </div>
    </div>
    <button type="button" class="menu-btn" id="menu-btn" aria-expanded={open} aria-controls="ctl" aria-label={t.menu} title={t.menu} onclick={() => (open = !open)}>
      <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="currentColor"><circle cx="5" cy="12" r="1.6" /><circle cx="12" cy="12" r="1.6" /><circle cx="19" cy="12" r="1.6" /></svg>
    </button>
  </div>
</header>
