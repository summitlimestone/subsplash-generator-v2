<script lang="ts">
  import { onMount } from 'svelte'
  import Editor from './Editor.svelte'
  import Jobs from './Jobs.svelte'
  import { app, connect, refresh } from './lib/state.svelte'

  let editing = $state<string | null>(null)
  let error = $state('')
  const job = $derived(editing ? app.jobs.find((j) => j.id === editing) : undefined)

  onMount(() => {
    refresh().catch((e) => (error = e.message))
    connect()
  })
</script>

<div class="shell">
  {#if !job}
    <nav>
      <span class="brand">Subsplash Generator</span>
      <span class="tab active">Jobs</span>
      <span class="spacer"></span>
      {#if !app.connected}<span class="warn">Reconnecting&hellip;</span>{/if}
    </nav>
  {/if}
  <main>
    {#if error}<p class="error">{error}</p>{/if}
    {#if job}
      {#key job.id}<Editor {job} onclose={() => (editing = null)} />{/key}
    {:else}
      <Jobs onedit={(id) => (editing = id)} />
    {/if}
  </main>
</div>

<style>
  .shell { display: flex; flex-direction: column; height: 100%; }
  nav { display: flex; align-items: center; gap: 18px; padding: 8px 14px; border-bottom: 1px solid var(--border); background: #1b1919; }
  .brand { font-weight: 700; color: var(--accent); }
  .tab { color: var(--muted); }
  .tab.active { color: var(--text); border-bottom: 2px solid var(--accent); padding-bottom: 2px; }
  .spacer { flex: 1; }
  .warn { color: var(--warning); font-size: 12px; }
  main { flex: 1; min-height: 0; overflow: auto; }
</style>
