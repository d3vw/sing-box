<script lang="ts" generics="V extends string">
  // A segmented switch; the indicator slides under the option picked.
  interface Props {
    options: { v: V; label: string }[];
    value: V;
    label?: string;
    id?: string;
    onpick: (v: V, button: HTMLButtonElement) => void;
  }
  let { options, value, label, id, onpick }: Props = $props();
  const index = $derived(Math.max(0, options.findIndex((o) => o.v === value)));
</script>

<div class="seg" role="radiogroup" aria-label={label} {id}>
  <i aria-hidden="true" style:transform="translateX({index * 100}%)"></i>
  {#each options as o (o.v)}
    <button type="button" role="radio" aria-checked={o.v === value} onclick={(e) => onpick(o.v, e.currentTarget)}>{o.label}</button>
  {/each}
</div>
