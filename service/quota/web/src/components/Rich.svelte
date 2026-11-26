<script lang="ts">
  // A sentence with emphasized parts; peak renders the hoverable peak hours.
  import type { Snippet } from "svelte";
  import type { Part } from "../lib/i18n";
  let { parts, peak }: { parts: Part[]; peak?: Snippet<[string]> } = $props();
</script>

{#each parts as p, i (i)}{#if typeof p === "string"}{p}{:else if p.accent && peak}{@render peak(p.b)}{:else}<span class={p.accent ? "accent" : "strong"}>{p.b}</span>{/if}{/each}
