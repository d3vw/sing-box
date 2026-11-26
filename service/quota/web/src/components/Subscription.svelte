<script lang="ts">
  import type { Subscription } from "../lib/types";
  import { tr } from "../lib/state.svelte";
  let { sub }: { sub: Subscription } = $props();
  const t = $derived(tr());
  let copied = $state(false);

  function copy() {
    const done = () => {
      copied = true;
      setTimeout(() => (copied = false), 1500);
    };
    // Outside a secure context (the portal is plain HTTP) the clipboard API
    // is missing; a selected textarea still copies.
    const fallback = () => {
      const a = document.createElement("textarea");
      a.value = sub.uri;
      a.style.position = "fixed";
      a.style.opacity = "0";
      document.body.appendChild(a);
      a.select();
      a.setSelectionRange(0, 99999);
      try { document.execCommand("copy"); } catch {}
      document.body.removeChild(a);
      done();
    };
    if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(sub.uri).then(done, fallback);
    else fallback();
  }
</script>

<span class="sec-title">{t.subscription}</span>
<div class="group sub">
  {#if sub.qr}<img class="qr" src={sub.qr} alt="QR code" />{/if}
  <span class="sub-hint">{t.subHint}</span>
  <button type="button" class="btn-sec" id="copy-sub" onclick={copy}>{copied ? t.copied : t.copy}</button>
</div>
