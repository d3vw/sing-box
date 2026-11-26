<script lang="ts">
  // A number and its unit, following its value like a spring; a dash
  // without a value.
  import { Spring } from "../lib/spring.svelte";
  interface Props {
    value: number | null;
    unit: string;
    digits?: number;
    play?: boolean;
  }
  let { value, unit, digits, play = true }: Props = $props();
  const spring = new Spring();
  $effect(() => {
    if (value === null) spring.reset();
    else spring.set(play ? value : 0);
  });
  const fmt = (x: number) => x.toFixed(digits != null ? digits : value !== null && value > 0 && value < 10 ? 1 : 0);
</script>

{#if value === null}
  <span class="nq-v none"><b>–</b><small>{unit}</small></span>
{:else}
  <span class="nq-v"><b data-ui-number style:filter={spring.blur || null}>{fmt(spring.x)}</b><small>{unit}</small></span>
{/if}
