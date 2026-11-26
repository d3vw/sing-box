<script lang="ts">
  import { tick } from "svelte";
  import type { Profile } from "../lib/types";
  import { findNode, latency, nodeAPI, nodes, parseSS, setNodes } from "../lib/outbound.svelte";
  import { clamp, reduced, tr } from "../lib/state.svelte";
  import { toast } from "../lib/toast.svelte";
  import { XIcon } from "../lib/icons";
  import CipherSelect from "./CipherSelect.svelte";

  let { methods }: { methods: string[] } = $props();
  const t = $derived(tr());
  const MAX_NODES = 20;

  const ICON = {
    test: '<svg aria-hidden="true" width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M9 1.8 3.5 9h4L7 14.2 12.5 7h-4z"/></svg>',
    edit: '<svg aria-hidden="true" width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M10.5 2.5l3 3L6 13H3v-3z"/></svg>',
    del: '<svg aria-hidden="true" width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M3 4.5h10M6.5 4.5V3h3v1.5M4.5 4.5l.6 8.5h5.8l.6-8.5"/></svg>',
  };

  // The latency test: good, fair or poor.
  const tone = (ms: number) => (ms < 150 ? "good" : ms < 400 ? "fair" : "poor");
  async function testOne(p: Profile) {
    latency[p.id] = { busy: true };
    try {
      const r = await nodeAPI("test", { id: p.id });
      latency[p.id] = r.ok && r.j.delay != null ? { ms: r.j.delay } : { err: r.j.error || t.requestFailed };
    } catch {
      latency[p.id] = { err: t.requestFailed };
    }
  }
  function testAll() {
    nodes.profiles.forEach((p) => { if (!latency[p.id]?.busy) testOne(p); });
  }

  let switching = $state<string | null>(null);
  async function activate(id: string) {
    if (id === nodes.active || switching !== null) return;
    switching = id;
    try {
      const r = await nodeAPI("activate", { id });
      if (!r.ok) {
        toast({ tone: "err", title: t.obSwitchFail, body: r.j.error || t.requestFailed });
        return;
      }
      setNodes(r.j);
      const p = findNode(id);
      toast({ tone: "ok", title: p ? t.obSwitched(p.name) : t.obSwitchedDefault, body: t.obReconnect });
    } catch {
      toast({ tone: "err", title: t.obSwitchFail, body: t.requestFailed });
    } finally {
      switching = null;
    }
  }
  async function remove(p: Profile) {
    if (!confirm(t.obDeleteConfirm(p.name))) return;
    try {
      const r = await nodeAPI("delete", { id: p.id });
      if (!r.ok) {
        toast({ tone: "err", title: t.requestFailed, body: r.j.error });
        return;
      }
      const was = nodes.active === p.id;
      setNodes(r.j);
      delete latency[p.id];
      if (editing === p.id) close(true);
      toast({ tone: "ok", title: t.obDeleted, body: was ? t.obBackToDefault : "" });
    } catch {
      toast({ tone: "err", title: t.requestFailed });
    }
  }

  let list = $state<HTMLElement>();
  function itemKey(e: KeyboardEvent, id: string) {
    if (e.key === " " || e.key === "Enter") {
      e.preventDefault();
      activate(id);
      return;
    }
    const items = Array.from(list!.querySelectorAll<HTMLElement>(".obp"));
    const i = items.indexOf(e.currentTarget as HTMLElement);
    const next = ({ ArrowDown: i + 1, ArrowRight: i + 1, ArrowUp: i - 1, ArrowLeft: i - 1 } as Record<string, number>)[e.key];
    if (next == null || e.target !== e.currentTarget) return;
    e.preventDefault();
    items[clamp(next, 0, items.length - 1)].focus();
  }

  // The editor adds or changes a node; pasting an ss:// link fills it in.
  interface Values {
    name: string;
    server: string;
    server_port: string;
    method: string;
    password: string;
    mux: boolean;
    padding: boolean;
  }
  type Why = "host" | "port" | "password";
  const blank = (): Values => ({ name: "", server: "", server_port: "", method: "2022-blake3-aes-128-gcm", password: "", mux: false, padding: false });
  let editing = $state<string | null>(null);
  let v = $state<Values>(blank());
  let saved = $state<Values>(blank());
  let st = $state<{ test: "idle" | "testing" | "ok" | "fail"; ms: number; why: string; whyKey: Why | "" }>({ test: "idle", ms: 0, why: "", whyKey: "" });
  let showPw = $state(false);
  let testing = $state(false);
  let saving = $state(false);
  let importText = $state("");
  let selOpen = $state(false);
  let select = $state<CipherSelect>();
  let form = $state<HTMLFormElement>();
  let nameInput = $state<HTMLInputElement>();
  let importInput = $state<HTMLInputElement>();

  const same = (a: Values, b: Values) => (Object.keys(a) as (keyof Values)[]).every((k) => a[k] === b[k]);
  const dirty = $derived(editing !== null && !same(v, saved));

  function invalid(x: Values): Why | "" {
    if (!x.server.trim()) return "host";
    const port = Number(x.server_port.trim());
    if (!/^\d+$/.test(x.server_port.trim()) || port < 1 || port > 65535) return "port";
    if (!x.password) return "password";
    return "";
  }
  function fail(why: string, key: Why | "" = "") {
    st.test = "fail";
    st.why = why;
    st.whyKey = key;
  }

  async function open(id: string) {
    if (editing !== null && dirty && editing !== id && !confirm(t.obDiscard)) return;
    const p = id ? findNode(id) : null;
    editing = p ? id : "";
    saved = p
      ? { name: p.name, server: p.server, server_port: String(p.server_port), method: p.method, password: p.password, mux: !!p.mux, padding: !!p.padding }
      : blank();
    v = { ...saved };
    st = { test: "idle", ms: 0, why: "", whyKey: "" };
    showPw = false;
    importText = "";
    await tick();
    if (matchMedia("(max-width:1000px)").matches) form?.scrollIntoView({ behavior: reduced ? "auto" : "smooth", block: "start" });
    (p ? nameInput : importInput)?.focus({ preventScroll: true });
  }
  function close(force: boolean) {
    if (!force && dirty && !confirm(t.obDiscard)) return;
    editing = null;
    select?.close();
  }

  function runImport() {
    const x = parseSS(importText);
    if (!x) {
      toast({ tone: "err", title: t.obImportBad });
      return;
    }
    v = { name: x.name || v.name, server: x.server, server_port: x.server_port, method: x.method, password: x.password, mux: v.mux, padding: v.padding };
    importText = "";
    st.test = "idle";
    toast({ tone: "ok", title: t.obImported });
  }

  async function runTest() {
    if (testing) return;
    const bad = invalid(v);
    if (bad) {
      fail(t.why[bad], bad);
      return;
    }
    testing = true;
    st.test = "testing";
    try {
      const r = await nodeAPI("test", { ...v });
      if (r.ok && r.j.delay != null) {
        st.test = "ok";
        st.ms = r.j.delay;
      } else fail(r.j.error || t.requestFailed);
    } catch {
      fail(t.requestFailed);
    } finally {
      testing = false;
    }
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (saving) return;
    const bad = invalid(v);
    if (bad) {
      fail(t.why[bad], bad);
      return;
    }
    const body: Record<string, string | boolean> = { ...v };
    if (editing) body.id = editing;
    const wasActive = !!editing && editing === nodes.active;
    saving = true;
    try {
      const r = await nodeAPI("save", body);
      if (!r.ok) {
        toast({ tone: "err", title: t.toastSaveFail, body: r.j.error || t.requestFailed });
        return;
      }
      setNodes(r.j);
      if (st.test === "ok" && r.j.id) latency[r.j.id] = { ms: st.ms };
      close(true);
      toast({ tone: "ok", title: t.obSaved, body: wasActive ? t.obReconnect : t.obPickHint });
    } catch {
      toast({ tone: "err", title: t.toastSaveFail, body: t.requestFailed });
    } finally {
      saving = false;
    }
  }

  const status = $derived(st.test === "ok" ? t.lampOk(st.ms) : st.test === "fail" ? t.lampFail : dirty ? t.obUnsaved : t.lampIdle);
  const invalidField = (k: Why) => st.test === "fail" && st.whyKey === k;
</script>

{#snippet item(p: Profile | null)}
  {@const id = p ? p.id : ""}
  {@const on = nodes.active === id}
  {@const l = p ? latency[p.id] : undefined}
  <div class="obp" class:on class:busy={switching === id} role="radio" aria-checked={on} tabindex={on ? 0 : -1} data-id={id}
    onclick={() => activate(id)} onkeydown={(e) => itemKey(e, id)}>
    <i class="obp-dot"></i>
    <div class="obp-main">
      <div class="obp-t"><b>{p ? p.name : t.obDefault}</b>{#if on}<span class="obp-use">{t.obInUse}</span>{/if}{#if switching === id}<span class="spin"></span>{/if}</div>
      <div class="obp-s">
        {#if p}
          <span class="num">{p.server}:{p.server_port}</span><span>{p.method}</span>
          {#if p.mux}<span class="tag">{t.mux}</span>{/if}{#if p.padding}<span class="tag">{t.padding}</span>{/if}
        {:else}<span>{t.obDefaultSub}</span>{/if}
      </div>
    </div>
    {#if p}
      {#if l?.busy}<span class="obp-lat"><span class="spin"></span></span>
      {:else if l?.err}<span class="obp-lat poor" title={l.err}>{t.obFail}</span>
      {:else if l && l.ms != null}<span class="obp-lat {tone(l.ms)}">{l.ms} ms</span>
      {:else}<span class="obp-lat"></span>{/if}
      <div class="obp-act">
        {#each [["test", t.obTest, () => testOne(p)], ["edit", t.obEdit, () => open(id)], ["del", t.obDelete, () => remove(p)]] as const as [kind, label, run] (kind)}
          <button type="button" class="obp-b" class:del={kind === "del"} title={label} aria-label={label + " " + p.name}
            onclick={(e) => { e.stopPropagation(); run(); }}>{@html ICON[kind]}</button>
        {/each}
      </div>
    {/if}
  </div>
{/snippet}

<section class="cxv obv" id="ob">
  <div class="cxv-hd">
    <div><h2>{t.obTitle}</h2><p>{t.obNote}</p></div>
    <div class="obv-tools">
      <button type="button" class="btn-sec ob-sm" id="ob-testall" hidden={!nodes.profiles.length} onclick={testAll}>{t.obTestAll}</button>
      <button type="button" class="btn-pri ob-sm" id="ob-add" disabled={nodes.profiles.length >= MAX_NODES} onclick={() => open("")}>{t.obAdd}</button>
    </div>
  </div>
  <div class="obv-body">
    <div class="obl" id="ob-list" role="radiogroup" aria-label={t.obTitle} bind:this={list}>
      {@render item(null)}
      {#each nodes.profiles as p (p.id)}{@render item(p)}{/each}
      {#if !nodes.profiles.length}<p class="obl-empty">{t.obEmpty}</p>{/if}
    </div>
    <form class="obe" id="ob-form" novalidate autocomplete="off" hidden={editing === null} bind:this={form} onsubmit={submit}
      onkeydown={(e) => { if (e.key === "Escape" && !selOpen) { e.preventDefault(); close(false); } }}>
      <div class="obe-hd"><h3 id="ob-form-title">{editing ? t.obEditTitle : t.obNewTitle}</h3><button type="button" class="toast-x" id="ob-close" aria-label={t.obCancel} onclick={() => close(false)}>{@html XIcon}</button></div>
      <div class="obe-imp">
        <input id="ob-import" placeholder={t.obImportPh} autocomplete="off" spellcheck="false" data-1p-ignore bind:value={importText} bind:this={importInput}
          onkeydown={(e) => { if (e.key === "Enter") { e.preventDefault(); runImport(); } }}
          onpaste={() => setTimeout(() => { if (parseSS(importText)) runImport(); })} />
        <button type="button" class="chip" id="ob-import-btn" onclick={runImport}>{t.obImport}</button>
      </div>
      <div class="group">
        <label class="row"><span>{t.obName}</span><input name="name" maxlength="40" placeholder={t.obNamePh} autocomplete="off" spellcheck="false" data-1p-ignore bind:value={v.name} bind:this={nameInput} /></label>
        <label class="row"><span>{t.server}</span><input name="server" placeholder={t.hostPlaceholder} autocomplete="off" spellcheck="false" data-1p-ignore aria-invalid={invalidField("host")} bind:value={v.server} oninput={() => (st.test = "idle")} /></label>
        <label class="row"><span>{t.port}</span><input name="server_port" placeholder={t.portPlaceholder} inputmode="numeric" autocomplete="off" data-1p-ignore aria-invalid={invalidField("port")} bind:value={v.server_port} oninput={() => (st.test = "idle")} /></label>
        <div class="row"><span>{t.cipher}</span><CipherSelect {methods} bind:value={v.method} bind:open={selOpen} bind:this={select} onchange={() => {}} /></div>
        <label class="row"><span>{t.password}</span><span class="pw"><input id="pw" type={showPw ? "text" : "password"} name="password" placeholder={t.passwordPlaceholder} autocomplete="off" data-1p-ignore aria-invalid={invalidField("password")} bind:value={v.password} /><button type="button" class="chip" id="pw-toggle" hidden={!v.password} onclick={(e) => { e.preventDefault(); showPw = !showPw; }}>{showPw ? t.hide : t.show}</button></span></label>
      </div>
      <div class="group">
        <label class="row sw"><span class="sw-t"><span>{t.mux}</span><span class="hint">{t.muxHint}</span></span><input type="checkbox" name="mux" value="true" role="switch" bind:checked={v.mux} /><i class="switch"></i></label>
        <label class="row sw"><span class="sw-t"><span>{t.padding}</span><span class="hint">{t.paddingHint}</span></span><input type="checkbox" name="padding" value="true" role="switch" bind:checked={v.padding} /><i class="switch"></i></label>
      </div>
      <div class="obe-ft">
        <span class="st {st.test}" id="ob-st" role="status"><i></i><span id="ob-st-text">{#if st.test === "testing"}<span class="shiny">{t.lampTesting}</span>{:else}{status}{/if}{#if st.test === "fail" && st.why}<span class="why"> · {st.why}</span>{/if}</span></span>
        <div class="obe-btns">
          <button type="button" class="btn-sec" id="ob-test" disabled={testing || !(v.server.trim() && v.server_port.trim())} onclick={runTest}>{#if testing}<span class="spin" aria-hidden="true"></span>{/if}{testing ? t.testing : t.test}</button>
          <button type="submit" class="btn-pri" id="ob-save" disabled={saving || (editing !== "" && !dirty)}>{#if saving}<span class="spin" aria-hidden="true"></span>{/if}{saving ? t.saving : t.save}</button>
        </div>
      </div>
    </form>
    <div class="obe-empty" id="ob-hint" hidden={editing !== null}><p>{t.obHint}</p></div>
  </div>
</section>
