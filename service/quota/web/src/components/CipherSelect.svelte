<script lang="ts">
  // The cipher picker: a button that opens a list, worked like a native
  // select with the arrow keys, Enter and Escape.
  import { clamp, dismissable, tr } from "../lib/state.svelte";
  let { methods, value = $bindable(), onchange, open = $bindable(false) }: { methods: string[]; value: string; onchange: () => void; open?: boolean } = $props();
  const t = $derived(tr());
  let act = $state(0);
  let up = $state(false);
  let box = $state<HTMLElement>();
  let btn = $state<HTMLButtonElement>();
  let list = $state<HTMLElement>();

  export function close(focus = false) {
    open = false;
    if (focus) btn?.focus();
  }
  function openList() {
    const r = btn!.getBoundingClientRect(), below = innerHeight - r.bottom;
    up = below < methods.length * 42 + 22 && r.top > below;
    open = true;
    act = Math.max(0, methods.indexOf(value));
    queueMicrotask(() => list?.focus({ preventScroll: true }));
  }
  function choose(i: number) {
    value = methods[i];
    close(true);
    onchange();
  }
  function listKey(e: KeyboardEvent) {
    if (e.key === "Tab") { close(); return; }
    const run = ({
      ArrowDown: () => (act = clamp(act + 1, 0, methods.length - 1)),
      ArrowUp: () => (act = clamp(act - 1, 0, methods.length - 1)),
      Home: () => (act = 0),
      End: () => (act = methods.length - 1),
      Enter: () => choose(act),
      " ": () => choose(act),
      Escape: () => close(true),
    } as Record<string, () => void>)[e.key];
    if (!run) return;
    e.preventDefault();
    e.stopPropagation();
    run();
  }
  $effect(() => dismissable(() => box, () => open, () => close()));
</script>

<div class="sel" class:up id="sel" bind:this={box}>
  <input type="hidden" name="method" {value} />
  <button type="button" class="sel-btn" aria-haspopup="listbox" aria-expanded={open} aria-controls="sel-list" aria-label={t.cipher} bind:this={btn}
    onclick={() => (open ? close() : openList())}
    onkeydown={(e) => { if (["ArrowDown", "ArrowUp", "Enter", " "].includes(e.key)) { e.preventDefault(); openList(); } }}>
    <span>{value}</span><svg aria-hidden="true" width="12" height="12" viewBox="0 0 12 12" fill="none"><path d="M3 4.5 6 7.5l3-3" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" /></svg>
  </button>
  <ul class="sel-list" id="sel-list" role="listbox" tabindex="-1" hidden={!open} aria-activedescendant="sel-o{act}" bind:this={list} onkeydown={listKey}>
    {#each methods as m, i (m)}
      <li role="option" id="sel-o{i}" aria-selected={m === value} class:act={i === act} onpointerenter={() => (act = i)} onclick={() => choose(i)} onkeydown={() => {}}><svg aria-hidden="true" width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="m3 7.5 2.5 2.5L11 4.5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>{m}</li>
    {/each}
  </ul>
</div>
