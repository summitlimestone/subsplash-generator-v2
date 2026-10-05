<script lang="ts">
  // The trim editor: the browser plays the recording itself, so seeking and
  // playback are instant with sound in sync.
  import { onMount, untrack } from 'svelte'
  import Timeline from './Timeline.svelte'
  import { api, ApiError, type Job } from './lib/api'
  import { app, onPeaks } from './lib/state.svelte'
  import { formatTime, parseTime } from './lib/time'

  let { job, onclose, onnext }: { job: Job; onclose: () => void; onnext?: (afterId: string) => void } = $props()

  let video: HTMLVideoElement
  let timeline = $state<Timeline>()
  let duration = $state(0)
  let fps = $state(30)
  let time = $state(0)
  let playing = $state(false)
  let rate = $state(1)
  // The editor works on a copy until Save.
  const initial = untrack(() => ({ ...job }))
  let start = $state<number | null>(initial.start)
  let end = $state<number | null>(initial.end)
  let date = $state(initial.date)
  // A backlog's recordings usually run in series, so a new one starts
  // with the series last used in the same folder.
  let series = $state(initial.series || (initial.backlog ? lastSeries(initial.backlog) : ''))
  const isBacklog = !!initial.backlog
  let peaks = $state<Uint8Array | null>(null)
  let peaksDone = $state(false)
  let message = $state('')
  let saving = $state(false)
  let startText = $state(formatTime(initial.start))
  let endText = $state(formatTime(initial.end))

  const dirty = $derived(start !== job.start || end !== job.end || date !== job.date || series !== job.series)
  const length = $derived(start != null && end != null ? end - start : null)
  const visibleSeries = $derived(app.series.filter((s) => !s.hidden || s.name === series))
  // A backlog may be marked on a machine without the series set up yet, so
  // its series is free text, suggesting known names and ones already used.
  const seriesSuggestions = $derived([
    ...new Set([...app.series.filter((s) => !s.hidden).map((s) => s.name), ...app.jobs.map((j) => j.series).filter(Boolean)]),
  ].sort())

  function lastSeries(dir: string): string {
    const used = app.jobs.filter((j) => j.backlog === dir && j.series && j.start != null)
    used.sort((a, b) => b.updated.localeCompare(a.updated))
    return used[0]?.series ?? ''
  }

  $effect(() => {
    startText = formatTime(start)
  })
  $effect(() => {
    endText = formatTime(end)
  })

  // Seeks are coalesced: while one is in flight, only the latest target is
  // kept, so scrubbing stays responsive on long recordings.
  let pendingSeek: number | null = null
  let watchdog: ReturnType<typeof setTimeout> | null = null
  // The playhead stays inside the selection (the whole recording until
  // the marks are set). Marks move outward by dragging or typing.
  const clamp = (t: number) => Math.max(start ?? 0, Math.min(end ?? (duration || t), t))
  function seek(t: number) {
    t = clamp(t)
    time = t
    if (video.seeking) {
      pendingSeek = t
      return
    }
    startSeek(t)
  }
  // A seek that never completes (seen when requests were starved) is
  // reissued rather than leaving the player stuck on an old frame.
  function startSeek(t: number) {
    video.currentTime = t
    if (watchdog) clearTimeout(watchdog)
    watchdog = setTimeout(() => {
      watchdog = null
      if (video.seeking) startSeek(pendingSeek ?? t)
    }, 4000)
  }
  function seeked() {
    if (pendingSeek != null) {
      const t = pendingSeek
      pendingSeek = null
      startSeek(t)
    } else if (watchdog) {
      clearTimeout(watchdog)
      watchdog = null
    }
  }

  // Follow the video smoothly while it plays, stopping at the end mark.
  function tick() {
    if (!video) return
    if (!video.paused && !video.seeking) {
      time = video.currentTime
      if (end != null && time >= end) {
        video.pause()
        seek(end)
      }
    }
    if (playing) requestAnimationFrame(tick)
  }

  function setRate(r: number) {
    rate = r
    video.playbackRate = r
  }
  function play() {
    // At the end mark, Play starts over from the start mark.
    if (end != null && time >= end - 0.05) seek(start ?? 0)
    video.playbackRate = rate
    video.play()
  }
  function goStart() {
    seek(start ?? 0)
    timeline?.show(time)
  }
  function goEnd() {
    seek(end ?? duration)
    timeline?.show(time)
  }
  function toggle() {
    if (video.paused) play()
    else video.pause()
  }
  const frame = () => 1 / (fps || 30)
  function step(frames: number) {
    video.pause()
    seek(Math.round((time + frames * frame()) * fps) / fps)
  }
  function jump(seconds: number) {
    seek(time + seconds)
    timeline?.show(time)
  }

  function setStart(t = time) {
    start = Math.round(t * 1000) / 1000
    if (end != null && end <= start) end = null
    if (time < start) seek(start)
  }
  function setEnd(t = time) {
    end = Math.round(t * 1000) / 1000
    if (start != null && start >= end) start = null
    if (time > end) seek(end)
  }
  // Dragging a handle shows the frame under it.
  function dragMark(which: 'start' | 'end', t: number) {
    video.pause()
    if (which === 'start') setStart(t)
    else setEnd(t)
    seek(which === 'start' ? start! : end!)
  }
  function commitText(which: 'start' | 'end') {
    const v = parseTime(which === 'start' ? startText : endText)
    if (v == null) {
      message = 'Times are HH:MM:SS.mmm'
      startText = formatTime(start)
      endText = formatTime(end)
      return
    }
    if (which === 'start') setStart(v)
    else setEnd(v)
    seek(v)
    timeline?.show(v)
  }

  async function save(): Promise<boolean> {
    if (start == null || end == null) {
      message = 'Set both the start and the end first.'
      return false
    }
    if (date && !/^\d{4}-\d{2}-\d{2}$/.test(date)) {
      message = 'The date is YYYY-MM-DD.'
      return false
    }
    saving = true
    try {
      job = await api.updateJob(job.id, { start, end, date, series })
      message = 'Saved'
      return true
    } catch (e) {
      message = (e as Error).message
      return false
    } finally {
      saving = false
    }
  }

  async function saveAndNext() {
    if (await save()) onnext?.(job.id)
  }

  async function skip() {
    try {
      job = await api.updateJob(job.id, { skipped: !job.skipped })
      if (job.skipped) onnext?.(job.id)
    } catch (e) {
      message = (e as Error).message
    }
  }

  async function saveAndTrim() {
    if (!(await save())) return
    try {
      await api.render([job.id], ['trim'])
      onclose()
    } catch (e) {
      const err = e as ApiError
      message = err.problems ? Object.values(err.problems).flat().join(' ') : err.message
    }
  }

  function close() {
    if (dirty && !confirm('Discard your unsaved changes?')) return
    onclose()
  }

  async function loadPeaks() {
    try {
      const r = await api.peaks(job.id)
      peaks = r.peaks
      peaksDone = r.complete
    } catch {
      peaksDone = true
    }
  }

  function key(e: KeyboardEvent) {
    const target = e.target as HTMLElement
    if (target.tagName === 'INPUT' || target.tagName === 'SELECT') {
      if (e.key === 'Enter') (target as HTMLInputElement).blur()
      return
    }
    const k = e.key
    const handled = true
    if (k === ' ') toggle()
    else if (e.shiftKey && (k === 'J' || k === 'j')) goStart()
    else if (e.shiftKey && (k === 'L' || k === 'l')) goEnd()
    else if (k === 'k' || k === 'K') toggle()
    // L plays, then doubles the speed up to 8x; J halves it back to 1x.
    else if (k === 'l' || k === 'L') {
      if (!video.paused) setRate(Math.min(rate * 2, 8))
      play()
    } else if (k === 'j' || k === 'J') setRate(Math.max(rate / 2, 1))
    else if (k === 'ArrowLeft') e.shiftKey ? jump(-1) : step(-1)
    else if (k === 'ArrowRight') e.shiftKey ? jump(1) : step(1)
    else if (k === 'i' || k === 'I') setStart()
    else if (k === 'o' || k === 'O') setEnd()
    else if (k === '+' || k === '=') timeline?.zoom(0.5)
    else if (k === '-') timeline?.zoom(2)
    else if (k === 'Enter') isBacklog ? saveAndNext() : save()
    else if (k === 'Escape') close()
    else return
    if (handled) e.preventDefault()
  }

  onMount(() => {
    api.probe(job.id).then((p) => (fps = p.fps || 30)).catch(() => {})
    loadPeaks()
    let timer: ReturnType<typeof setTimeout> | null = null
    const off = onPeaks((rec) => {
      if (rec !== job.recording || timer) return
      timer = setTimeout(() => ((timer = null), loadPeaks()), 1000)
    })
    window.addEventListener('keydown', key)
    return () => {
      off()
      window.removeEventListener('keydown', key)
    }
  })

  function loaded() {
    duration = video.duration
    const t = start ?? 0
    seek(t)
  }
</script>

<div class="editor">
  <header>
    <button onclick={close}>&larr; Back</button>
    <div class="title">
      <strong>{job.recording.split(/[\\/]/).pop()}</strong>
    </div>
    <label>Date <input type="text" bind:value={date} placeholder="YYYY-MM-DD" size="10" /></label>
    <label>
      Series
      {#if isBacklog}
        <input bind:value={series} list="series-options" placeholder="Series name" size="18" />
        <datalist id="series-options">{#each seriesSuggestions as name}<option value={name}></option>{/each}</datalist>
      {:else}
        <select bind:value={series}>
          <option value="">(none)</option>
          {#each visibleSeries as s (s.name)}<option value={s.name}>{s.name}</option>{/each}
        </select>
      {/if}
    </label>
    {#if isBacklog}
      <button onclick={skip} disabled={saving} title="This recording has no sermon to cut">{job.skipped ? 'Unskip' : 'Skip'}</button>
      <button onclick={save} disabled={saving || !dirty}>Save</button>
      <button class="accent" onclick={saveAndNext} disabled={saving} title="Save and mark the next recording (Enter)">Save &amp; next</button>
    {:else}
      <button onclick={save} disabled={saving || !dirty}>Save</button>
      <button class="accent" onclick={saveAndTrim} disabled={saving}>Save &amp; trim</button>
    {/if}
  </header>

  <div class="stage">
    <!-- svelte-ignore a11y_media_has_caption -->
    <video
      bind:this={video}
      src={api.videoURL(job.id)}
      preload="auto"
      onloadedmetadata={loaded}
      onseeked={seeked}
      onplay={() => ((playing = true), requestAnimationFrame(tick))}
      onpause={() => ((playing = false), (time = clamp(video.currentTime)), setRate(1))}
      onclick={toggle}
    ></video>
  </div>

  <div class="transport">
    <div class="controls">
      <button class="icon" onclick={goStart} title="Go to the start mark (Shift+J)" aria-label="Go to start">
        <svg viewBox="0 0 16 16"><rect x="2.5" y="3" width="2" height="10" rx="0.8" /><path d="M13 3.6v8.8c0 .5-.6.8-1 .5L6 8.5a.6.6 0 0 1 0-1l6-4.4c.4-.3 1 0 1 .5z" /></svg>
      </button>
      <button class="icon" onclick={() => jump(-10)} title="Back 10 s" aria-label="Back 10 seconds">
        <svg viewBox="0 0 16 16"><path d="M8 3.8v8.4c0 .5-.6.8-1 .5L1.6 8.5a.6.6 0 0 1 0-1L7 3.3c.4-.3 1 0 1 .5zM14.5 3.8v8.4c0 .5-.6.8-1 .5L8.1 8.5a.6.6 0 0 1 0-1l5.4-4.2c.4-.3 1 0 1 .5z" /></svg>
      </button>
      <button class="icon" onclick={() => step(-1)} title="Back one frame (&larr;)" aria-label="Back one frame">
        <svg viewBox="0 0 16 16"><path d="M10 3.5 5.5 8l4.5 4.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
      </button>
      <button class="icon play" onclick={toggle} title={playing ? 'Pause (Space)' : 'Play (Space)'} aria-label={playing ? 'Pause' : 'Play'}>
        {#if playing}
          <svg viewBox="0 0 16 16"><rect x="3.5" y="2.5" width="3.2" height="11" rx="1" /><rect x="9.3" y="2.5" width="3.2" height="11" rx="1" /></svg>
        {:else}
          <svg viewBox="0 0 16 16"><path d="M4.5 2.8v10.4c0 .6.6.9 1.1.6l8.1-5.2c.5-.3.5-.9 0-1.2L5.6 2.2c-.5-.3-1.1 0-1.1.6z" /></svg>
        {/if}
      </button>
      <button class="icon" onclick={() => step(1)} title="Forward one frame (&rarr;)" aria-label="Forward one frame">
        <svg viewBox="0 0 16 16"><path d="M6 3.5 10.5 8 6 12.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
      </button>
      <button class="icon" onclick={() => jump(10)} title="Forward 10 s" aria-label="Forward 10 seconds">
        <svg viewBox="0 0 16 16"><path d="M8 3.8v8.4c0 .5.6.8 1 .5l5.4-4.2a.6.6 0 0 0 0-1L9 3.3c-.4-.3-1 0-1 .5zM1.5 3.8v8.4c0 .5.6.8 1 .5l5.4-4.2a.6.6 0 0 0 0-1L2.5 3.3c-.4-.3-1 0-1 .5z" /></svg>
      </button>
      <button class="icon" onclick={goEnd} title="Go to the end mark (Shift+L)" aria-label="Go to end">
        <svg viewBox="0 0 16 16"><rect x="11.5" y="3" width="2" height="10" rx="0.8" /><path d="M3 3.6v8.8c0 .5.6.8 1 .5l6-4.4a.6.6 0 0 0 0-1L4 3.1c-.4-.3-1 0-1 .5z" /></svg>
      </button>
    </div>
    <span class="clock mono">{formatTime(time)}</span>
    {#if rate !== 1 && playing}<span class="muted">{rate}x</span>{/if}
    <span class="spacer"></span>
    <div class="mark start">
      <button onclick={() => setStart()} title="Set start at the playhead (I)">Set start</button>
      <input class="mono" bind:value={startText} onchange={() => commitText('start')} size="12" />
    </div>
    <div class="mark end">
      <button onclick={() => setEnd()} title="Set end at the playhead (O)">Set end</button>
      <input class="mono" bind:value={endText} onchange={() => commitText('end')} size="12" />
    </div>
    <span class="length muted">{length != null ? formatTime(length, false) : ''}</span>
  </div>

  {#if duration > 0}
    <Timeline
      bind:this={timeline}
      jobId={job.id}
      {duration}
      {time}
      {start}
      {end}
      {peaks}
      peaksPerSecond={app.info?.peaksPerSecond ?? 20}
      onseek={(t) => seek(t)}
      onstart={(t) => dragMark('start', t)}
      onend={(t) => dragMark('end', t)}
    />
  {/if}

  <footer class="muted">
    {#if message}<span class="msg">{message}</span>{/if}
    {#if !peaksDone}
      <span>Loading waveform{duration > 0 && peaks ? ` ${Math.min(99, Math.floor((peaks.length / (duration * (app.info?.peaksPerSecond ?? 20))) * 100))}%` : ''}&hellip;</span>
    {/if}
    <span class="keys">Space or K play/pause &middot; L faster &middot; J slower &middot; &larr;&rarr; frame &middot; Shift+&larr;&rarr; 1 s &middot; I/O set start/end &middot; Shift+J/L go to start/end &middot; wheel zoom &middot; Enter {isBacklog ? 'save & next' : 'save'}</span>
  </footer>
</div>

<style>
  .editor { display: flex; flex-direction: column; height: 100%; gap: 8px; padding: 10px 14px; }
  header { display: flex; align-items: center; gap: 10px; }
  header .title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  header label { display: flex; align-items: center; gap: 6px; color: var(--muted); }
  .stage { flex: 1; min-height: 0; display: flex; justify-content: center; background: #000; border-radius: 6px; }
  video { max-width: 100%; max-height: 100%; }
  .transport { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  /* One height for every control on the row. */
  .transport button, .transport input { height: 32px; }
  .transport input { width: 118px; text-align: center; }
  .controls { display: flex; gap: 4px; }
  .icon { width: 36px; padding: 0; display: inline-flex; align-items: center; justify-content: center; }
  .icon svg { width: 16px; height: 16px; fill: currentColor; }
  .clock { font-size: 18px; margin-left: 6px; min-width: 128px; }
  .spacer { flex: 1; }
  .mark { display: flex; align-items: center; gap: 4px; padding-left: 8px; border-left: 3px solid; height: 32px; }
  .mark.start { border-color: var(--start); }
  .mark.end { border-color: var(--end); }
  .length { min-width: 64px; text-align: right; }
  footer { display: flex; gap: 16px; font-size: 12px; }
  footer .keys { margin-left: auto; }
  footer .msg { color: var(--text); }
</style>
