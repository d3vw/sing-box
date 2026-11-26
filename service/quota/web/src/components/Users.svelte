<script lang="ts">
  // Every user, for admins, each with a reset.
  import type { PageUser } from "../lib/types";
  import { post, refresh, tr } from "../lib/state.svelte";
  import { toast } from "../lib/toast.svelte";
  let { users }: { users: PageUser[] } = $props();
  const t = $derived(tr());

  async function reset(e: SubmitEvent, u: PageUser) {
    e.preventDefault();
    if (!confirm(t.resetConfirm)) return;
    try {
      const r = await post("/quota/reset", new URLSearchParams({ tag: u.tag, name: u.name }));
      if (!r.ok) {
        toast({ tone: "err", title: t.requestFailed, body: r.j.error });
        return;
      }
      toast({ tone: "ok", title: t.resetDone(u.name) });
      refresh();
    } catch {
      toast({ tone: "err", title: t.requestFailed });
    }
  }
</script>

<div class="users">
  <span class="sec-title">{t.allUsers}</span>
  <div class="group">
    {#each users as u (u.tag + "/" + u.name)}
      <div class="urow">
        <div class="u-name">
          <span class="u-user">{u.name}</span><span class="tag">{u.tag}</span>
          {#if u.blocked}<span class="u-blk">{t.blocked}</span>{/if}
          {#if u.validUntil}<span class="u-exp" class:bad={u.expired}>{u.expired ? t.expiredOn(u.validUntil) : t.validTo(u.validUntil)}</span>{/if}
          {#if u.conns}<span class="u-cx">{t.cxCount(u.conns)}</span>{/if}
        </div>
        <div class="u-usage"><span class="num">{u.used} / {u.quota}</span><div class="u-bar" style="--p:{u.p}%;--pu:{u.pu}%"><i></i></div></div>
        <form method="post" action="/quota/reset" onsubmit={(e) => reset(e, u)}>
          <input type="hidden" name="tag" value={u.tag} /><input type="hidden" name="name" value={u.name} />
          <button type="submit" class="chip">{t.reset}</button>
        </form>
      </div>
    {/each}
  </div>
</div>
