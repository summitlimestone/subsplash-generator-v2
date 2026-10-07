<script lang="ts">
  import { onMount } from 'svelte'
  import Backlog from './Backlog.svelte'
  import Editor from './Editor.svelte'
  import Jobs from './Jobs.svelte'
  import Live from './Live.svelte'
  import Series from './Series.svelte'
  import Settings from './Settings.svelte'
  import Welcome from './Welcome.svelte'
  import { app, connect, nextToMark, refresh } from './lib/state.svelte'

  type Tab = 'live' | 'jobs' | 'backlog' | 'series' | 'settings'
  const tabs: [Tab, string][] = [['live', 'Live'], ['jobs', 'Jobs'], ['backlog', 'Bulk edit'], ['series', 'Series'], ['settings', 'Settings']]
  let tab = $state<Tab>('live')
  let backlogDir = $state('')
  let editing = $state<string | null>(null)
  let error = $state('')
  let notice = $state('')
  let welcomed = $state(false)
  const job = $derived(editing ? app.jobs.find((j) => j.id === editing) : undefined)

  // After a backlog recording is saved or skipped, go straight on to the next.
  function next(afterId: string) {
    const dir = app.jobs.find((j) => j.id === afterId)?.backlog
    const n = dir ? nextToMark(dir, afterId) : undefined
    if (n) editing = n.id
    else {
      editing = null
      notice = 'Every recording in this folder is marked.'
    }
  }

  function open(id: string) {
    notice = ''
    editing = id
  }

  onMount(() => {
    refresh().catch((e) => (error = e.message))
    connect()
  })
</script>

{#if app.info && !app.info.welcomed && !welcomed}
  <Welcome ondone={() => ((welcomed = true), (tab = 'live'))} />
{/if}

<div class="shell">
  {#if !job}
    <nav>
      <span class="brand">Subsplash Generator</span>
      {#each tabs as [id, label]}
        <button class="tab" class:active={tab === id} onclick={() => ((tab = id), (notice = ''))}>{label}</button>
      {/each}
      <span class="spacer"></span>
      {#if !app.connected}<span class="warn">Reconnecting&hellip;</span>{/if}
    </nav>
  {/if}
  <main>
    {#if error}<p class="error">{error}</p>{/if}
    {#if job}
      {#key job.id}<Editor {job} onclose={() => (editing = null)} onnext={next} />{/key}
    {:else}
      {#if notice}<p class="notice">{notice}</p>{/if}
      {#if tab === 'live'}
        <Live onedit={open} />
      {:else if tab === 'jobs'}
        <Jobs onedit={open} />
      {:else if tab === 'backlog'}
        <Backlog onedit={open} bind:current={backlogDir} />
      {:else if tab === 'series'}
        <Series />
      {:else}
        <Settings />
      {/if}
    {/if}
  </main>
</div>

<style>
  .shell { display: flex; flex-direction: column; height: 100%; }
  nav { display: flex; align-items: center; gap: 6px; padding: 6px 14px; border-bottom: 1px solid var(--border); background: #1b1919; }
  .brand { font-weight: 700; color: var(--accent); margin-right: 12px; }
  .tab { background: none; border: none; border-radius: 0; color: var(--muted); padding: 4px 8px; border-bottom: 2px solid transparent; }
  .tab:hover { color: var(--text); }
  .tab.active { color: var(--text); border-bottom-color: var(--accent); }
  .spacer { flex: 1; }
  .warn { color: var(--warning); font-size: 12px; }
  .notice { margin: 12px 14px 0; padding: 8px 12px; background: #2f3a24; border: 1px solid var(--accent); border-radius: 6px; }
  main { flex: 1; min-height: 0; overflow: auto; }
</style>
