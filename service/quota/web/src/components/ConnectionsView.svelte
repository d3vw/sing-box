<script lang="ts">
  import type { ConnRecord } from "../lib/types";
  import { age, conns, fmtBytes } from "../lib/conns.svelte";
  import { post, tr } from "../lib/state.svelte";
  import { Spring } from "../lib/spring.svelte";
  import { toast } from "../lib/toast.svelte";
  import Seg from "./Seg.svelte";

  let { initial }: { initial: { active: number; closed: number } } = $props();
  const t = $derived(tr());
  const MAX = 500, TOP = 8;
  const XSVG = '<svg aria-hidden="true" width="12" height="12" viewBox="0 0 12 12" fill="none"><path d="M2.5 2.5l7 7M9.5 2.5l-7 7" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>';

  type SortKey = "host" | "upload" | "download" | "time";
  let tab = $state<"active" | "closed">("active");
  let net = $state<"" | "tcp" | "udp">("");
  let sort = $state<{ k: SortKey; d: "asc" | "desc" }>({ k: "time", d: "desc" });
  let q = $state("");
  let list = $state<HTMLElement>();
  const closing = $state<Record<string, boolean>>({});

  // svelte-ignore state_referenced_locally
  const rollA = new Spring(initial.active), rollC = new Spring(initial.closed);
  $effect(() => {
    if (!conns.snap) return;
    rollA.set(conns.snap.active_count);
    rollC.set(conns.snap.closed_total);
  });

  const snap = $derived(conns.snap);
  const kept = $derived(snap?.closed ? snap.closed.length : 0);
  const when = (c: ConnRecord) => (c.active ? c.created_at : c.closed_at || 0);
  const picked = $derived.by(() => {
    const src = snap ? (tab === "active" ? snap.active : snap.closed) || [] : [];
    const base = net ? src.filter((c) => c.network === net) : src;
    const f = q.trim().toLowerCase();
    const shown = f ? base.filter((c) => [c.host, c.dest, c.protocol, c.network, c.outbound, c.rule].join(" ").toLowerCase().includes(f)) : base.slice();
    const k = sort.k, dir = sort.d === "asc" ? 1 : -1;
    const val = (c: ConnRecord) => (k === "host" ? c.host : k === "time" ? when(c) : c[k]);
    shown.sort((a, b) => {
      const x = val(a), y = val(b);
      if (x < y) return -dir;
      if (x > y) return dir;
      return +b.id - +a.id;
    });
    return { base, shown, f };
  });
  const items = $derived(picked.shown.slice(0, MAX));

  // The side column sums the list (not the search) by destination; a click
  // filters by it, another clears it.
  const hosts = $derived.by(() => {
    const by: Record<string, { host: string; n: number; b: number }> = {};
    const keys: { host: string; n: number; b: number }[] = [];
    picked.base.forEach((c) => {
      let g = by[c.host];
      if (!g) { g = by[c.host] = { host: c.host, n: 0, b: 0 }; keys.push(g); }
      g.n++;
      g.b += c.upload + c.download;
    });
    keys.sort((a, b) => b.b - a.b || b.n - a.n);
    return keys.slice(0, TOP);
  });
  const hostTop = $derived(Math.max(1, hosts[0]?.b || 0));

  // The same column again flips the order; another starts A to Z for the
  // destination and largest first otherwise.
  function sortBy(k: SortKey) {
    sort = sort.k === k ? { k, d: sort.d === "asc" ? "desc" : "asc" } : { k, d: k === "host" ? "asc" : "desc" };
  }
  const ariaSort = (k: SortKey) => (sort.k === k ? (sort.d === "asc" ? "ascending" : "descending") : "none");

  async function closeConns(body: string, many: boolean) {
    try {
      const r = await post("/connections/close", body);
      if (!r.ok) {
        toast({ tone: "err", title: t.requestFailed, body: r.j.error });
        return;
      }
      if (many) toast({ title: t.toastConnClosed(r.j.closed || 0) });
    } catch {
      toast({ tone: "err", title: t.requestFailed });
    }
  }
  const more = $derived(picked.shown.length - MAX);
</script>

{#snippet sortHead(k: SortKey, label: string)}
  <button type="button" data-dir={sort.k === k ? sort.d : undefined} onclick={() => sortBy(k)}><span>{label}</span><i class="cx-ar"></i></button>
{/snippet}

<section class="cxv" id="cx">
  <div class="cxv-hd">
    <div><h2>{t.cxTitle}</h2><p>{t.cxNote}</p></div>
    <span class="cx-live" class:on={conns.online} id="cx-live"><i></i><span>{conns.online ? t.cxLive : t.cxOffline}</span></span>
  </div>
  <div class="cxv-stats">
    <div class="cxv-stat"><span>{t.cxActive}</span><b id="cx-a" data-ui-number style:filter={rollA.blur || null}>{Math.round(rollA.x)}</b></div>
    <div class="cxv-stat"><span>{t.cxClosedTotal}</span><b id="cx-c" data-ui-number style:filter={rollC.blur || null}>{Math.round(rollC.x)}</b></div>
    <div class="cxv-stat"><span>{t.cxUp}</span><b id="cx-u">{snap ? fmtBytes(snap.upload) : "–"}</b></div>
    <div class="cxv-stat"><span>{t.cxDown}</span><b id="cx-d">{snap ? fmtBytes(snap.download) : "–"}</b></div>
  </div>
  <div class="cxv-bar">
    <Seg id="cx-tabs" value={tab}
      options={[{ v: "active", label: t.cxTabActive(snap ? snap.active_count : "") }, { v: "closed", label: t.cxTabClosed(snap ? kept : "") }]}
      onpick={(v) => { if (tab === v) return; tab = v; if (list) list.scrollTop = 0; }} />
    <Seg id="cx-net" value={net} options={[{ v: "", label: t.cxNetAll }, { v: "tcp", label: "TCP" }, { v: "udp", label: "UDP" }]} onpick={(v) => (net = v)} />
    <input class="cx-q" id="cx-q" type="search" placeholder={t.cxSearch} autocomplete="off" spellcheck="false" data-1p-ignore bind:value={q} />
    <button type="button" class="btn-sec cx-all" id="cx-all" hidden={tab !== "active" || !snap || !snap.active_count}
      onclick={() => { if (confirm(t.cxCloseAllConfirm)) closeConns("all=true", true); }}>{t.cxCloseAll}</button>
  </div>
  <div class="cxv-body">
    <div class="cx-tbl">
      <div class="cx-tr cx-th" id="cx-th">
        <span class="cx-h" aria-sort={ariaSort("host")}>{@render sortHead("host", t.cxColHost)}</span>
        <span class="cx-p">{t.cxColProto}</span>
        <span class="cx-o">{t.cxColOut}</span>
        <span class="cx-c r cx-u" aria-sort={ariaSort("upload")}>{@render sortHead("upload", t.cxColUp)}</span>
        <span class="cx-c r cx-d" aria-sort={ariaSort("download")}>{@render sortHead("download", t.cxColDown)}</span>
        <span class="cx-c r cx-t" aria-sort={ariaSort("time")}>{@render sortHead("time", t.cxColTime)}</span>
        <span></span>
      </div>
      <div class="cx-rows" id="cx-list" role="list" bind:this={list}>
        {#each items as c (tab + ":" + c.id)}
          <div class="cx-tr" role="listitem">
            <div class="cx-c cx-h"><b title={c.host}>{c.host}</b><small title={c.dest}>{c.dest}</small></div>
            <div class="cx-c cx-p"><span class="tag">{c.protocol || c.network}</span>{#if c.protocol && c.network}<span class="tag">{c.network}</span>{/if}</div>
            <div class="cx-c cx-o"><span title={c.outbound || ""}>{c.outbound || "–"}</span><small title={c.rule || ""}>{c.rule || ""}</small></div>
            <div class="cx-c r cx-u">{fmtBytes(c.upload)}</div>
            <div class="cx-c r cx-d">{fmtBytes(c.download)}</div>
            <div class="cx-c r cx-t">{c.active ? t.cxFor(t.cxDur(age(c.created_at))) : t.cxAgo(t.cxDur(age(c.closed_at || 0)))}</div>
            <button type="button" class="cx-x" class:off={!c.active} tabindex={c.active ? 0 : -1} aria-label={t.cxClose} title={t.cxClose}
              disabled={!!closing[tab + ":" + c.id]}
              onclick={() => { closing[tab + ":" + c.id] = true; closeConns("id=" + encodeURIComponent(c.id), false); }}>{@html XSVG}</button>
          </div>
        {/each}
        {#if !items.length}<div class="cx-empty">{picked.f || net ? t.cxNoMatch : tab === "active" ? t.cxEmptyActive : t.cxEmptyClosed}</div>{/if}
      </div>
    </div>
    <aside class="cxv-side">
      <h3>{t.cxTop}</h3>
      <div class="cx-hosts" id="cx-hosts">
        {#each hosts as g (g.host)}
          <button type="button" class="cx-hb" class:on={picked.f === g.host.toLowerCase()} onclick={() => (q = picked.f === g.host.toLowerCase() ? "" : g.host)}>
            <b title={g.host}>{g.host}</b><span>{fmtBytes(g.b)}</span><small>{t.cxTopN(g.n)}</small><i style="--p:{Math.max(2, (g.b / hostTop) * 100)}%"></i>
          </button>
        {:else}
          <div class="cx-empty">{t.cxTopEmpty}</div>
        {/each}
      </div>
    </aside>
  </div>
  <div class="cx-foot">
    <span id="cx-count">{snap ? t.cxShown(picked.shown.length, picked.base.length) : ""}</span>
    <span id="cx-more">{more > 0 ? t.cxMore(more) : tab === "closed" && snap && snap.closed_total > kept ? t.cxKept(kept) : ""}</span>
  </div>
</section>
