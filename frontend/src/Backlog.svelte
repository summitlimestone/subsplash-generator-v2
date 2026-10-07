<script lang="ts">
  // Marking a folder of past recordings: pick the folder, then Mark next
  // walks through every recording that still needs its sermon marked.
  import { onMount } from 'svelte'
  import { api, type BacklogSummary } from './lib/api'
  import { app, backlogJobs, isMarked, nextToMark } from './lib/state.svelte'
  import { formatTime } from './lib/time'
  import { SvelteSet } from 'svelte/reactivity'
  import SeriesDialog from './SeriesDialog.svelte'

  let { onedit, current = $bindable() }: { onedit: (id: string) => void; current: string } = $props()

  let summaries = $state<BacklogSummary[]>([])
  let error = $state('')
  let manualDir = $state('')
  const selected = new SvelteSet<string>()
  let settingSeries = $state(false)

  async function setSeries(series: string) {
    settingSeries = false
    try {
      await api.editJobs([...selected], { series })
      selected.clear()
    } catch (e) {
      error = (e as Error).message
    }
  }

  function toggleAll() {
    if (selected.size === list.length) selected.clear()
    else list.forEach((j) => selected.add(j.id))
  }

  const list = $derived(current ? backlogJobs(current) : [])
  const marked = $derived(list.filter(isMarked).length)
  const skipped = $derived(list.filter((j) => j.skipped).length)
  const remaining = $derived(list.length - marked - skipped)
  const summary = $derived(summaries.find((s) => s.dir === current))
  const rel = (p: string) => (current && p.startsWith(current) ? p.slice(current.length + 1) : p).replaceAll('\\', '/')

  async function load() {
    try {
      summaries = await api.backlogs()
      if (!summaries.some((s) => s.dir === current)) current = summaries[0]?.dir ?? ''
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function choose() {
    error = ''
    let dir = manualDir.trim()
    if (app.info?.canOpenFiles) {
      try {
        dir = (await api.openFolder('Choose the folder of recordings')).path
      } catch (e) {
        error = (e as Error).message
        return
      }
    }
    if (!dir) return
    try {
      const s = await api.openBacklog(dir)
      manualDir = ''
      await load()
      current = s.dir
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function forget() {
    if (!confirm('Remove this folder from the app? Its marks stay saved in the folder.')) return
    await api.forgetBacklog(current).catch((e) => (error = (e as Error).message))
    current = ''
    await load()
  }

  function markNext() {
    const j = nextToMark(current)
    if (j) onedit(j.id)
  }

  onMount(load)
</script>

<div class="backlog">
  {#if summaries.length === 0}
    <div class="intro">
      <h2>Mark a folder of recordings</h2>
      <p class="muted">Choose the folder that holds the recordings. Subfolders are included. You'll go through them one at a time and mark where each sermon starts and ends.</p>
      {#if !app.info?.canOpenFiles}<input bind:value={manualDir} placeholder="Path to the folder" size="50" />{/if}
      <button class="accent big" onclick={choose}>Choose folder</button>
    </div>
  {:else}
    <div class="toolbar">
      <select bind:value={current}>
        {#each summaries as s (s.dir)}<option value={s.dir}>{s.dir}</option>{/each}
      </select>
      {#if !app.info?.canOpenFiles}<input bind:value={manualDir} placeholder="Path to another folder" size="30" />{/if}
      <button onclick={choose}>Open another folder</button>
      <button onclick={load} title="Look for recordings added since">Refresh</button>
      <span class="spacer"></span>
      <button class="small danger" onclick={forget}>Remove folder</button>
    </div>

    {#if summary?.missing}
      <p class="error">This folder isn't there. If it's on a removed drive, reconnect it and press Refresh.</p>
    {:else}
      <div class="progress">
        <div class="count">
          <strong>{marked} of {list.length - skipped}</strong> marked
          {#if skipped}<span class="muted">&middot; {skipped} skipped</span>{/if}
        </div>
        <div class="bar"><div style="width:{list.length - skipped ? (marked / (list.length - skipped)) * 100 : 0}%"></div></div>
        {#if selected.size}
          <span class="muted">{selected.size} selected</span>
          <button onclick={() => (settingSeries = true)}>Set series&hellip;</button>
        {/if}
        <button class="accent big" onclick={markNext} disabled={remaining === 0}>
          {remaining === 0 ? 'All marked' : `Mark next (${remaining} left)`}
        </button>
      </div>

      <table>
        <thead><tr><th class="check"><input type="checkbox" checked={list.length > 0 && selected.size === list.length} onchange={toggleAll} /></th><th></th><th>Recording</th><th>Date</th><th>Series</th><th class="sermon">Sermon</th></tr></thead>
        <tbody>
          {#each list as j (j.id)}
            <tr onclick={() => onedit(j.id)} class:done={isMarked(j)} class:skipped={j.skipped} class:selected={selected.has(j.id)}>
              <td class="check" onclick={(e) => e.stopPropagation()}>
                <input type="checkbox" checked={selected.has(j.id)} onchange={() => (selected.has(j.id) ? selected.delete(j.id) : selected.add(j.id))} />
              </td>
              <td class="tick">{j.skipped ? '–' : isMarked(j) ? '✓' : ''}</td>
              <td><div class="clip" title={j.recording}>{rel(j.recording)}</div></td>
              <td class="nowrap">{j.date}</td>
              <td class="nowrap">{j.series}</td>
              <td class="mono sermon">
                {#if j.skipped}<span class="muted">skipped</span>
                {:else if isMarked(j)}{formatTime(j.start, false)} – {formatTime(j.end, false)}{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  {/if}
  {#if error}<p class="error">{error}</p>{/if}
</div>

{#if settingSeries}
  <SeriesDialog count={selected.size} freeText onapply={setSeries} oncancel={() => (settingSeries = false)} />
{/if}

<style>
  .backlog { padding: 12px 14px; display: flex; flex-direction: column; gap: 12px; }
  .intro { max-width: 520px; margin: 60px auto; text-align: center; display: flex; flex-direction: column; gap: 12px; align-items: center; }
  .intro h2 { margin: 0; }
  .toolbar { display: flex; gap: 8px; align-items: center; }
  .toolbar select { flex: 0 1 520px; min-width: 0; }
  .toolbar button, .big { white-space: nowrap; }
  .spacer { flex: 1; }
  .big { font-size: 16px; padding: 8px 20px; }
  .progress { display: flex; align-items: center; gap: 16px; padding: 12px; background: var(--surface-2); border: 1px solid var(--border); border-radius: 8px; }
  .count { white-space: nowrap; }
  .progress .bar { flex: 1; height: 8px; background: var(--surface); border-radius: 4px; overflow: hidden; }
  .progress .bar div { height: 100%; background: var(--accent); transition: width 0.3s; }
  table { width: 100%; border-collapse: collapse; }
  th { text-align: left; color: var(--muted); font-weight: 500; font-size: 12px; padding: 4px 8px; border-bottom: 1px solid var(--border); }
  td { padding: 7px 8px; border-bottom: 1px solid #2b2928; cursor: pointer; }
  tr:hover td { background: #2a2828; }
  .check { width: 28px; cursor: default; }
  tr.selected td { background: #2a2a24; }
  .tick { width: 24px; color: var(--accent); font-weight: 700; }
  tr.skipped td { color: var(--muted); }
  /* Long recording paths are cut short so the table never scrolls sideways. */
  .clip { max-width: 40vw; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .nowrap, .mono { white-space: nowrap; }
  @media (max-width: 860px) { .sermon { display: none; } }
</style>
