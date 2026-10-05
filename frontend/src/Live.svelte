<script lang="ts">
  // The live service: watch OBS, mark the sermon, then trim and stitch.
  // In compact form it is the OBS dock.
  import { api, ApiError, type Step } from './lib/api'
  import { app } from './lib/state.svelte'
  import { formatTime } from './lib/time'

  let { compact = false, onedit }: { compact?: boolean; onedit?: (id: string) => void } = $props()

  let error = $state('')
  let busy = $state(false)
  let preview = $state(false)

  const live = $derived(app.live)
  const job = $derived(live?.jobId ? app.jobs.find((j) => j.id === live.jobId) : undefined)
  const progress = $derived(job ? app.progress[job.id] : undefined)
  const rendering = $derived(!!job && ['queued', 'trimming', 'stitching'].includes(job.status))
  const seriesNames = $derived(app.series.filter((s) => !s.hidden).map((s) => s.name))
  const lastSlide = $derived(live?.slides?.[0])

  const status = $derived.by(() => {
    if (!live) return { text: 'Starting…', tone: 'muted' }
    if (!live.obsConfigured) return { text: 'Set up OBS in Settings', tone: 'warn' }
    if (!live.obsConnected) return { text: 'Connecting to OBS…', tone: 'warn' }
    if (job && rendering) return { text: job.status === 'stitching' ? 'Stitching…' : job.status === 'queued' ? 'Queued…' : 'Trimming…', tone: 'info' }
    if (live.phase === 'recording') return { text: `Recording ${formatTime(live.elapsed, false)}`, tone: 'rec' }
    if (live.phase === 'waiting') return { text: live.recording ? 'Recording' : 'Waiting for OBS to record', tone: 'info' }
    if (live.phase === 'stopped') {
      if (job?.status === 'done') return { text: 'Done', tone: 'ok' }
      if (job?.status === 'trimmed') return { text: 'Trimmed: check it, then stitch', tone: 'ok' }
      if (job?.status === 'failed') return { text: 'Failed', tone: 'bad' }
      return { text: 'Recording finished', tone: 'ok' }
    }
    return { text: live.recording ? 'OBS is recording (not watching)' : 'Not watching', tone: 'muted' }
  })

  async function act(fn: () => Promise<unknown>) {
    error = ''
    busy = true
    try {
      await fn()
    } catch (e) {
      const err = e as ApiError
      error = err.problems ? Object.values(err.problems).flat().join(' ') : err.message
    } finally {
      busy = false
    }
  }

  const render = (steps: Step[]) => act(() => api.render([job!.id], steps))
  const canMark = $derived(live?.phase === 'recording' && live.obsConnected)
  const canTrim = $derived(!!job && !rendering && job.start != null && job.end != null && !!job.recording)
  const canStitch = $derived(!!job && !rendering && !!job.trimmed)
</script>

<div class="live" class:compact>
  <div class="status tone-{status.tone}">{status.text}</div>

  {#if !compact}
    <div class="connections">
      <span class="conn" class:ok={live?.obsConnected} title={live?.obsError}>OBS {live?.obsConnected ? 'connected' : live?.obsConfigured ? 'not connected' : 'not set up'}</span>
      <span class="conn" class:ok={live?.ppConnected} title={live?.ppError}>ProPresenter {live?.ppConnected ? 'connected' : live?.ppConfigured ? 'not connected' : 'not set up'}</span>
      {#if lastSlide}<span class="muted slide" title={lastSlide.uid}>Slide: {lastSlide.text || '(no text)'}</span>{/if}
    </div>
  {/if}

  {#if live?.notice}<div class="notice">{live.notice}</div>{/if}

  <select value={live?.series ?? ''} onchange={(e) => act(() => api.liveSeries((e.target as HTMLSelectElement).value))} title="The series whose intro and outro this service gets">
    <option value="">(choose a series)</option>
    {#each seriesNames as name}<option value={name}>{name}</option>{/each}
  </select>

  {#if live?.watching}
    <button class="wide" disabled={busy} onclick={() => act(() => api.liveStop())}>Stop watching</button>
  {:else}
    <button class="accent wide" disabled={busy || !live?.obsConfigured} onclick={() => act(() => api.liveStart(live?.series ?? ''))}>Start watching</button>
  {/if}

  <div class="row">
    {#each ['start', 'end'] as const as which}
      {@const value = which === 'start' ? job?.start : job?.end}
      <div class="mark {which}">
        <button disabled={!canMark || busy} onclick={() => act(() => api.liveMark(which))}>Mark {which === 'start' ? 'start' : 'end'}</button>
        <div class="value mono">{value != null ? formatTime(value, false) : '--:--:--'}</div>
      </div>
    {/each}
  </div>

  <div class="row">
    <button disabled={!canTrim || busy} onclick={() => render(['trim'])}>Trim</button>
    <button disabled={!canStitch || busy} onclick={() => render(['stitch'])}>Stitch</button>
  </div>

  {#if progress}
    <div class="bar"><div style="width:{Math.round(progress.fraction * 100)}%"></div></div>
    <div class="muted small">{progress.label} {progress.speed}</div>
  {/if}

  {#if !compact && job}
    <div class="row secondary">
      {#if onedit && job.recording}<button onclick={() => onedit(job.id)}>Edit marks</button>{/if}
      {#if job.trimmed}<button onclick={() => (preview = true)}>Check trim</button>{/if}
      {#if rendering}<button onclick={() => act(() => api.cancel(job.id))}>Cancel</button>{/if}
    </div>
    {#if job.recording}<div class="muted small file" title={job.recording}>{job.recording}</div>{/if}
  {/if}

  {#if job?.error}<div class="error small">{job.error.split('\n')[0]}</div>{/if}
  {#if error}<div class="error small">{error}</div>{/if}
</div>

{#if preview && job}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="modal" onclick={() => (preview = false)}>
    <!-- svelte-ignore a11y_media_has_caption -->
    <video src={api.videoURL(job.id, true)} controls autoplay onclick={(e) => e.stopPropagation()}></video>
  </div>
{/if}

<style>
  .live { display: flex; flex-direction: column; gap: 10px; padding: 16px; max-width: 560px; margin: 0 auto; }
  .live.compact { gap: 4px; padding: 6px; max-width: none; }
  .status { font-size: 20px; font-weight: 700; }
  .compact .status { font-size: 16px; margin-bottom: 2px; }
  .tone-rec { color: #ff6b5e; }
  .tone-ok { color: var(--accent); }
  .tone-info { color: #8fc1ea; }
  .tone-warn { color: var(--warning); }
  .tone-bad { color: #ff8a80; }
  .tone-muted { color: var(--muted); }
  .connections { display: flex; gap: 12px; flex-wrap: wrap; font-size: 13px; }
  .conn { color: var(--warning); }
  .conn.ok { color: var(--accent); }
  .slide { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 260px; }
  .notice { background: #3a2e14; border: 1px solid var(--warning); border-radius: 6px; padding: 8px 10px; font-size: 13px; }
  select, .wide { width: 100%; }
  .wide { padding: 8px; font-size: 15px; }
  .compact .wide { padding: 6px; font-size: 14px; }
  .row { display: flex; gap: 6px; }
  .row > button, .row > .mark { flex: 1; }
  .mark { display: flex; flex-direction: column; gap: 4px; }
  .mark > button { width: 100%; }
  .mark.start > button { border-left: 3px solid var(--start); }
  .mark.end > button { border-left: 3px solid var(--end); }
  .value { text-align: center; font-size: 15px; }
  .compact .value { font-size: 12px; }
  .secondary { flex-wrap: wrap; }
  .secondary > button { flex: 0 0 auto; }
  .bar { height: 4px; background: var(--surface); border-radius: 2px; overflow: hidden; }
  .bar div { height: 100%; background: var(--info); transition: width 0.3s; }
  .small { font-size: 12px; }
  .file { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .modal { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.7); display: flex; align-items: center; justify-content: center; z-index: 10; }
  .modal video { width: min(1100px, 92vw); max-height: 80vh; background: #000; }
</style>
