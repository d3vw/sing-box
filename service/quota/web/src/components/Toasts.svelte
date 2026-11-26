<script lang="ts">
  import { dismiss, toasts } from "../lib/toast.svelte";
  import { tr } from "../lib/state.svelte";
  import { XIcon } from "../lib/icons";
  const t = $derived(tr());
</script>

<div class="toasts" id="toasts" aria-live="polite">
  {#each toasts as o (o.id)}
    <div class="toast" class:err={o.tone === "err"} role="status">
      <i></i>
      <div class="toast-b"><b>{o.title}</b>{#if o.body}<span>{o.body}</span>{/if}</div>
      {#if o.action}
        {@const action = o.action}
        <button type="button" class="link" onclick={() => { dismiss(o.id); action.run(); }}>{action.label}</button>
      {/if}
      <button type="button" class="toast-x" aria-label={t.dismiss} title={t.dismiss} onclick={() => dismiss(o.id)}>{@html XIcon}</button>
    </div>
  {/each}
</div>
