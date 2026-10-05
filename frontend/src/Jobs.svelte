<script lang="ts">
  import { SvelteSet } from 'svelte/reactivity'
  import { api, ApiError, type Job, type Step } from './lib/api'
  import { app, isMarked } from './lib/state.svelte'
  import { formatTime } from './lib/time'

  let { onedit }: { onedit: (id: string) => void } = $props()

  const selected = new SvelteSet<string>()
  let problems = $state<string[]>([])
  let manualPath = $state('')
  let preview = $state<Job | null>(null)

  // Recordings from the Bulk edit tab show up here once they are marked.
  const shown = $derived(app.jobs.filter((j) => !j.backlog || (isMarked(j) && !j.skipped)))
  const rendering = (j: Job) => ['queued', 'trimming', 'stitching'].includes(j.status)
  const canTrim = (j: Job) => !rendering(j) && j.start != null && j.end != null && !!j.recording
  const canStitch = (j: Job) => !rendering(j) && !!j.trimmed
  const name = (j: Job) => j.date ? j.stem : '(no date)'
  const file = (p: string) => p.split(/[\\/]/).pop()

  const statusLabel: Record<string, string> = {
    draft: 'needs marks', ready: 'ready to trim', queued: 'queued', trimming: 'trimming',
    trimmed: 'check trim', stitching: 'stitching', done: 'done', failed: 'failed',
  }

  async function add() {
    problems = []
    let path = manualPath.trim()
    if (app.info?.canOpenFiles) {
      try {
        path = (await api.openFile('Choose a recording', ['*.mkv', '*.mp4', '*.mov'])).path
      } catch (e) {
        problems = [(e as Error).message]
        return
      }
    }
    if (!path) return
    try {
      const job = await api.createJob({ recording: path })
      manualPath = ''
      onedit(job.id)
    } catch (e) {
      problems = [(e as Error).message]
    }
  }

  async function render(ids: string[], steps: Step[]) {
    problems = []
    try {
      await api.render(ids, steps)
      selected.clear()
    } catch (e) {
      const err = e as ApiError
      if (err.problems) {
        problems = Object.entries(err.problems).flatMap(([id, list]) => {
          const j = app.jobs.find((x) => x.id === id)
          return list.map((p) => `${j ? name(j) : id}: ${p}`)
        })
      } else problems = [err.message]
    }
  }

  async function remove(j: Job) {
    if (!confirm(`Remove ${name(j)} from the list? Rendered files are kept.`)) return
    await api.deleteJob(j.id).catch((e) => (problems = [(e as Error).message]))
    selected.delete(j.id)
  }

  function toggleAll() {
    if (selected.size === shown.length) selected.clear()
    else shown.forEach((j) => selected.add(j.id))
  }
</script>

<div class="jobs">
  <div class="toolbar">
    {#if !app.info?.canOpenFiles}
      <input bind:value={manualPath} placeholder="Path to a recording" size="50" />
    {/if}
    <button class="accent" onclick={add}>Add recording</button>
    <span class="spacer"></span>
    {#if selected.size > 0}
      <span class="muted">{selected.size} selected</span>
      <button onclick={() => render([...selected], ['trim'])}>Trim</button>
      <button onclick={() => render([...selected], ['stitch'])}>Stitch</button>
      <button onclick={() => render([...selected], ['trim', 'stitch'])}>Trim &amp; stitch</button>
    {/if}
  </div>

  {#if problems.length}
    <div class="problems">
      <button class="small close" onclick={() => (problems = [])}>&times;</button>
      {#each problems as p}<div>{p}</div>{/each}
    </div>
  {/if}

  {#if shown.length === 0}
    <p class="empty muted">No jobs yet. Add a recording to mark its sermon.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th><input type="checkbox" checked={selected.size === shown.length} onchange={toggleAll} /></th>
          <th>Video</th>
          <th>Series</th>
          <th>Sermon</th>
          <th>Status</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each shown as j (j.id)}
          {@const p = app.progress[j.id]}
          <tr class:selected={selected.has(j.id)}>
            <td><input type="checkbox" checked={selected.has(j.id)} onchange={() => (selected.has(j.id) ? selected.delete(j.id) : selected.add(j.id))} /></td>
            <td>
              <div>{name(j)}</div>
              <div class="muted small" title={j.recording}>{file(j.recording)}</div>
            </td>
            <td>{j.series || '—'}</td>
            <td class="mono">
              {#if j.start != null && j.end != null}
                {formatTime(j.start, false)} – {formatTime(j.end, false)}
                <div class="muted small">{formatTime(j.end - j.start, false)} long</div>
              {:else}<span class="muted">—</span>{/if}
            </td>
            <td>
              <span class="status s-{j.status}">{statusLabel[j.status] ?? j.status}</span>
              {#if p}
                <div class="bar"><div style="width:{Math.round(p.fraction * 100)}%"></div></div>
                <div class="muted small">{p.label} {p.speed}</div>
              {/if}
              {#if j.error}<div class="error small" title={j.error}>{j.error.split('\n')[0]}</div>{/if}
            </td>
            <td class="actions">
              {#if rendering(j)}
                <button class="small" onclick={() => api.cancel(j.id)}>Cancel</button>
              {:else}
                <button class="small" onclick={() => onedit(j.id)}>Edit</button>
                <button class="small" onclick={() => render([j.id], ['trim'])} disabled={!canTrim(j)}>Trim</button>
                {#if j.trimmed}<button class="small" onclick={() => (preview = j)}>Check</button>{/if}
                <button class="small" onclick={() => render([j.id], ['stitch'])} disabled={!canStitch(j)}>Stitch</button>
                <button class="small danger" onclick={() => remove(j)} title="Remove from the list">&times;</button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

{#if preview}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="modal" onclick={() => (preview = null)}>
    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
    <div class="panel" onclick={(e) => e.stopPropagation()}>
      <div class="panel-head">
        <strong>Trimmed: {name(preview)}</strong>
        <button class="small" onclick={() => (preview = null)}>Close</button>
      </div>
      <!-- svelte-ignore a11y_media_has_caption -->
      <video src={api.videoURL(preview.id, true)} controls autoplay></video>
    </div>
  </div>
{/if}

<style>
  .jobs { padding: 12px 14px; display: flex; flex-direction: column; gap: 10px; }
  .toolbar { display: flex; gap: 8px; align-items: center; }
  .spacer { flex: 1; }
  .problems { position: relative; background: #3a1f1c; border: 1px solid var(--danger); border-radius: 6px; padding: 8px 36px 8px 12px; }
  .problems .close { position: absolute; top: 6px; right: 6px; }
  .empty { margin-top: 40px; text-align: center; }
  table { width: 100%; border-collapse: collapse; }
  th { text-align: left; color: var(--muted); font-weight: 500; font-size: 12px; padding: 4px 8px; border-bottom: 1px solid var(--border); }
  td { padding: 8px; border-bottom: 1px solid #2b2928; vertical-align: top; }
  tr.selected td { background: #2a2a24; }
  .small { font-size: 12px; }
  .actions { white-space: nowrap; text-align: right; }
  .actions button { margin-left: 4px; }
  .status { display: inline-block; padding: 1px 8px; border-radius: 10px; font-size: 12px; background: var(--surface); }
  .s-ready { background: #2f3a24; }
  .s-queued, .s-trimming, .s-stitching { background: #22384d; color: #bcd6ef; }
  .s-trimmed { background: #4a3c15; color: #f0d78a; }
  .s-done { background: #3d5a1c; color: #d6efb0; }
  .s-failed { background: #5a1f1a; color: #ffb3a8; }
  .s-draft { color: var(--muted); }
  .bar { height: 4px; background: var(--surface); border-radius: 2px; margin-top: 4px; width: 140px; overflow: hidden; }
  .bar div { height: 100%; background: var(--info); transition: width 0.3s; }
  .modal { position: fixed; inset: 0; background: rgba(0,0,0,0.7); display: flex; align-items: center; justify-content: center; z-index: 10; }
  .panel { background: var(--bg); border: 1px solid var(--border); border-radius: 8px; padding: 10px; width: min(1100px, 92vw); }
  .panel-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
  .panel video { width: 100%; max-height: 75vh; background: #000; }
</style>
