<script lang="ts" module>
  export interface TipContent {
    title: string;
    value: string;
    note?: string | null;
    warn?: boolean;
  }
</script>

<script lang="ts">
  // A chart's readout next to the point looked at.
  let { tip, style = "", id }: { tip: TipContent | null; style?: string; id?: string } = $props();
</script>

<div class="tip" {id} role="status" hidden={!tip} {style}>
  <span class="tip-t">{tip?.title}</span><span class="tip-v">{tip?.value}</span><span class="tip-n" class:warn={!!tip?.warn} hidden={!tip?.note}>{tip?.note || ""}</span>
</div>
