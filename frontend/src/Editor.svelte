<script lang="ts">
  // The trim editor: the browser plays the recording itself, so seeking and
  // playback are instant with sound in sync.
  import { onMount, untrack } from 'svelte'
  import Timeline from './Timeline.svelte'
  import { api, ApiError, type Job } from './lib/api'
  import { app, onPeaks } from './lib/state.svelte'
  import { formatTime, parseTime } from './lib/time'

  let { job, onclose }: { job: Job; onclose: () => void } = $props()

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
  let series = $state(initial.series)
  let peaks = $state<Uint8Array | null>(null)
  let peaksDone = $state(false)
  let message = $state('')
  let saving = $state(false)
  let startText = $state(formatTime(initial.start))
  let endText = $state(formatTime(initial.end))

  const dirty = $derived(start !== job.start || end !== job.end || date !== job.date || series !== job.series)
  const length = $derived(start != null && end != null ? end - start : null)
  const visibleSeries = $derived(app.series.filter((s) => !s.hidden || s.name === series))

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
  function seek(t: number) {
    t = Math.max(0, Math.min(duration || t, t))
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

  // Follow the video smoothly while it plays.
  function tick() {
    if (!video) return
    if (!video.paused && !video.seeking) time = video.currentTime
    if (playing) requestAnimationFrame(tick)
  }

  function play() {
    video.playbackRate = rate
    video.play()
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
  }
  function setEnd(t = time) {
    end = Math.round(t * 1000) / 1000
    if (start != null && start >= end) start = null
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

  // Play the moments around the cuts to check them.
  function previewStart() {
    if (start == null) return
    seek(start)
    timeline?.show(start)
    play()
  }
  function previewEnd() {
    if (end == null) return
    seek(Math.max(0, end - 5))
    timeline?.show(end)
    play()
    const stopAt = end
    const check = () => {
      if (video.currentTime >= stopAt) video.pause()
      else if (!video.paused) requestAnimationFrame(check)
    }
    requestAnimationFrame(check)
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
    else if (k === 'k' || k === 'K') video.pause()
    else if (k === 'l' || k === 'L') {
      rate = video.paused ? 1 : Math.min(rate * 2, 8)
      play()
    } else if (k === 'j' || k === 'J') jump(-10)
    else if (k === 'ArrowLeft') e.shiftKey ? jump(-1) : step(-1)
    else if (k === 'ArrowRight') e.shiftKey ? jump(1) : step(1)
    else if (k === 'i' || k === 'I') setStart()
    else if (k === 'o' || k === 'O') setEnd()
    else if (k === 'Home' && start != null) (seek(start), timeline?.show(start))
    else if (k === 'End' && end != null) (seek(end), timeline?.show(end))
    else if (k === '+' || k === '=') timeline?.zoom(0.5)
    else if (k === '-') timeline?.zoom(2)
    else if (k === 'Enter') save()
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
    <button onclick={close}>&larr; Jobs</button>
    <div class="title">
      <strong>{job.recording.split(/[\\/]/).pop()}</strong>
    </div>
    <label>Date <input type="text" bind:value={date} placeholder="YYYY-MM-DD" size="10" /></label>
    <label>
      Series
      <select bind:value={series}>
        <option value="">(none)</option>
        {#each visibleSeries as s (s.name)}<option value={s.name}>{s.name}</option>{/each}
      </select>
    </label>
    <button onclick={save} disabled={saving || !dirty}>Save</button>
    <button class="accent" onclick={saveAndTrim} disabled={saving}>Save &amp; trim</button>
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
      onpause={() => ((playing = false), (time = video.currentTime))}
      onclick={toggle}
    ></video>
  </div>

  <div class="transport">
    <button onclick={() => jump(-10)} title="Back 10 s (J)">&laquo; 10s</button>
    <button onclick={() => step(-1)} title="Back one frame (&larr;)">&lsaquo;</button>
    <button class="play" onclick={toggle} title="Play/pause (Space)">{playing ? 'Pause' : 'Play'}</button>
    <button onclick={() => step(1)} title="Forward one frame (&rarr;)">&rsaquo;</button>
    <button onclick={() => jump(10)} title="Forward 10 s">10s &raquo;</button>
    <span class="clock mono">{formatTime(time)}</span>
    {#if rate !== 1 && playing}<span class="muted">{rate}x</span>{/if}
    <span class="spacer"></span>
    <div class="mark start">
      <button onclick={() => setStart()} title="Set start at the playhead (I)">Set start</button>
      <input class="mono" bind:value={startText} onchange={() => commitText('start')} size="12" />
      <button class="small" onclick={previewStart} disabled={start == null} title="Play from the start">&#9654;</button>
    </div>
    <div class="mark end">
      <button onclick={() => setEnd()} title="Set end at the playhead (O)">Set end</button>
      <input class="mono" bind:value={endText} onchange={() => commitText('end')} size="12" />
      <button class="small" onclick={previewEnd} disabled={end == null} title="Play the last 5 s">&#9654;</button>
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
      onstart={(t) => setStart(t)}
      onend={(t) => setEnd(t)}
    />
  {/if}

  <footer class="muted">
    {#if message}<span class="msg">{message}</span>{/if}
    {#if !peaksDone}
      <span>Loading waveform{duration > 0 && peaks ? ` ${Math.min(99, Math.floor((peaks.length / (duration * (app.info?.peaksPerSecond ?? 20))) * 100))}%` : ''}&hellip;</span>
    {/if}
    <span class="keys">Space play &middot; &larr;&rarr; frame &middot; Shift+&larr;&rarr; 1 s &middot; J/K/L &middot; I/O set start/end &middot; Home/End go to marks &middot; wheel zoom &middot; Enter save</span>
  </footer>
</div>

<style>
  .editor { display: flex; flex-direction: column; height: 100%; gap: 8px; padding: 10px 14px; }
  header { display: flex; align-items: center; gap: 10px; }
  header .title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  header label { display: flex; align-items: center; gap: 6px; color: var(--muted); }
  .stage { flex: 1; min-height: 0; display: flex; justify-content: center; background: #000; border-radius: 6px; }
  video { max-width: 100%; max-height: 100%; }
  .transport { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
  .transport .play { min-width: 64px; }
  .clock { font-size: 18px; margin-left: 8px; }
  .spacer { flex: 1; }
  .mark { display: flex; align-items: center; gap: 4px; padding-left: 8px; border-left: 3px solid; }
  .mark.start { border-color: var(--start); }
  .mark.end { border-color: var(--end); }
  .length { min-width: 64px; text-align: right; }
  footer { display: flex; gap: 16px; font-size: 12px; }
  footer .keys { margin-left: auto; }
  footer .msg { color: var(--text); }
</style>
