<script lang="ts">
  // The OBS dock: the Live controls alone, for OBS's Custom Browser Docks.
  import { onMount } from 'svelte'
  import Live from './Live.svelte'
  import { app, connect, refresh } from './lib/state.svelte'

  onMount(() => {
    refresh().catch(() => {})
    connect()
  })
</script>

{#if !app.connected}<div class="offline">Not connected to the app</div>{/if}
<Live compact />

<style>
  .offline { background: #3a2e14; color: var(--warning); font-size: 12px; padding: 2px 6px; }
</style>
