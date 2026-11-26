<script lang="ts">
  import { onMount } from "svelte";
  import { page, refresh, tr, ui, type View } from "./lib/state.svelte";
  import { conns, startConns, want } from "./lib/conns.svelte";
  import { findNode, nodes, setNodes } from "./lib/outbound.svelte";
  import Header from "./components/Header.svelte";
  import MainView from "./components/MainView.svelte";
  import OutboundView from "./components/OutboundView.svelte";
  import ConnectionsView from "./components/ConnectionsView.svelte";
  import Toasts from "./components/Toasts.svelte";
  import Users from "./components/Users.svelte";

  const t = $derived(tr());
  const data = $derived(page.data);
  const root = document.documentElement;

  // The page has tabs: traffic, outbound and connections, the latter two
  // only where the portal offers them. #outbound and #connections in the
  // address open them directly.
  const hashes: Partial<Record<View, string>> = { ob: "#outbound", cx: "#connections" };
  const has = (v: View) => v === "main" || (v === "ob" && !!data?.outbound) || (v === "cx" && !!data?.conns);
  function fromHash(): View {
    for (const [k, h] of Object.entries(hashes)) if (location.hash === h) return k as View;
    return "main";
  }
  function show(v: View, push: boolean) {
    if (!has(v)) v = "main";
    ui.view = v;
    if (push) {
      try {
        history.replaceState(null, "", hashes[v] || location.pathname + location.search);
      } catch {}
    }
  }
  ui.view = fromHash();
  $effect(() => {
    if (data && !has(ui.view)) ui.view = "main";
  });
  $effect(() => {
    for (const [k] of Object.entries(hashes)) root.classList.toggle("on-" + k, ui.view === k);
    want();
  });

  $effect(() => {
    document.title = (data?.title ? data.title + " — " : "") + t.title;
  });
  // The outbound tab keeps its nodes between refreshes; a refresh brings in
  // changes made elsewhere.
  $effect(() => {
    if (data?.outbound) setNodes(data.outbound);
  });
  $effect(() => {
    if (data?.conns) startConns();
  });
  const activeNode = $derived(findNode(nodes.active));

  onMount(() => {
    if (!page.data) refresh();
    const onHash = () => show(fromHash(), false);
    window.addEventListener("hashchange", onHash);
    // Refresh the data every minute while the page is in view, and the
    // "updated n min ago" lines every 30 seconds.
    const data = setInterval(() => {
      if (!document.hidden) refresh();
    }, 60000);
    const clock = setInterval(() => (ui.now = Date.now()), 30000);
    const onVisible = () => {
      if (!document.hidden && Date.now() - page.loadedAt > 60000) refresh();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      window.removeEventListener("hashchange", onHash);
      document.removeEventListener("visibilitychange", onVisible);
      clearInterval(data);
      clearInterval(clock);
    };
  });
</script>

<div class="shell"><div class="pg">
  <Header self={data?.self ?? null} />
  {#if data}
    {#if data.outbound || data.conns}
      <nav class="vt" id="vt" role="tablist">
        <button type="button" role="tab" aria-selected={ui.view === "main"} aria-controls="view-main" onclick={() => show("main", true)}><span>{t.vtMain}</span></button>
        {#if data.outbound}
          <button type="button" role="tab" aria-selected={ui.view === "ob"} aria-controls="view-ob" onclick={() => show("ob", true)}><span>{t.vtOut}</span><span class="vt-n vt-name" id="vt-ob" hidden={!activeNode}>{activeNode?.name ?? ""}</span></button>
        {/if}
        {#if data.conns}
          <button type="button" role="tab" aria-selected={ui.view === "cx"} aria-controls="view-cx" onclick={() => show("cx", true)}><span>{t.vtConns}</span><span class="vt-n" id="vt-n">{conns.snap ? conns.snap.active_count : data.conns.active}</span></button>
        {/if}
      </nav>
    {/if}
    <div class="view" id="view-main" role="tabpanel">
      {#if !data.self && !data.users?.length}<div class="msg"><div class="card">{t.noQuota}</div></div>{/if}
      {#if data.self}<MainView {data} self={data.self} />{/if}
      {#if data.users?.length}<Users users={data.users} />{/if}
    </div>
    {#if data.outbound}
      <div class="view" id="view-ob" role="tabpanel"><OutboundView methods={data.outbound.methods} /></div>
    {/if}
    {#if data.conns}
      <div class="view" id="view-cx" role="tabpanel"><ConnectionsView initial={data.conns} /></div>
    {/if}
  {/if}
</div></div>
<Toasts />
