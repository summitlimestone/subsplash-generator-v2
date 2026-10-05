<script lang="ts">
  // The editor's timeline: a time ruler, filmstrip and audio waveform, with
  // draggable start/end handles and the playhead. The wheel zooms around
  // the cursor; Shift+wheel pans.
  import { onMount } from 'svelte'
  import { api } from './lib/api'
  import { formatTime } from './lib/time'

  let {
    jobId,
    duration,
    time,
    start,
    end,
    peaks,
    peaksPerSecond,
    onseek,
    onstart,
    onend,
  }: {
    jobId: string
    duration: number
    time: number
    start: number | null
    end: number | null
    peaks: Uint8Array | null
    peaksPerSecond: number
    onseek: (t: number, scrubbing: boolean) => void
    onstart: (t: number) => void
    onend: (t: number) => void
  } = $props()

  const RULER = 20
  const STRIP = 54
  const WAVE = 80
  const HEIGHT = RULER + STRIP + WAVE
  const THUMB_W = Math.round((STRIP * 16) / 9)

  let canvas: HTMLCanvasElement
  let wrap: HTMLDivElement
  let width = $state(800)
  let viewStart = $state(0)
  let viewEnd = $state(0)
  let drag: 'start' | 'end' | 'scrub' | null = null
  let hover = $state<number | null>(null)
  const thumbs = new Map<string, HTMLImageElement>()

  $effect(() => {
    if (duration > 0 && viewEnd === 0) viewEnd = duration
  })

  // Stretch the waveform over this recording's own range of levels, so
  // quiet speech and loud music differ visibly.
  const range = $derived.by(() => {
    if (!peaks || peaks.length < 10) return { lo: 0, hi: 255 }
    const sample: number[] = []
    const stride = Math.max(1, Math.floor(peaks.length / 5000))
    for (let i = 0; i < peaks.length; i += stride) if (peaks[i] > 0) sample.push(peaks[i])
    if (sample.length < 10) return { lo: 0, hi: 255 }
    sample.sort((a, b) => a - b)
    const lo = sample[Math.floor(sample.length * 0.05)]
    const hi = sample[Math.floor(sample.length * 0.995)]
    return hi - lo < 10 ? { lo: 0, hi: 255 } : { lo, hi }
  })

  const span = () => Math.max(viewEnd - viewStart, 0.001)
  const toX = (t: number) => ((t - viewStart) / span()) * width
  const toT = (x: number) => Math.min(duration, Math.max(0, viewStart + (x / width) * span()))

  onMount(() => {
    const ro = new ResizeObserver(() => (width = wrap.clientWidth))
    ro.observe(wrap)
    width = wrap.clientWidth
    return () => ro.disconnect()
  })

  // Redraw whenever anything drawn changes.
  $effect(() => {
    void [width, viewStart, viewEnd, time, start, end, peaks, range, hover, duration]
    draw()
  })

  function niceStep(seconds: number): number {
    for (const s of [0.1, 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600]) if (s >= seconds) return s
    return 7200
  }

  // Thumbnails load at most two at a time, newest first, and ones scrolled
  // out of view are dropped: the browser allows only six connections to
  // the app, and the video needs them more.
  const MAX_LOADING = 2
  let loading = 0
  let wanted: string[] = []

  function thumb(t: number): HTMLImageElement | null {
    const url = api.thumbURL(jobId, t, STRIP * 2)
    const img = thumbs.get(url)
    if (img) return img.complete && img.naturalWidth > 0 ? img : null
    wanted.push(url)
    return null
  }

  function pump() {
    while (loading < MAX_LOADING && wanted.length) {
      const url = wanted.shift()!
      if (thumbs.has(url)) continue
      const img = new Image()
      thumbs.set(url, img)
      loading++
      const done = () => {
        loading--
        if (img.naturalWidth === 0) thumbs.delete(url)
        draw()
      }
      img.onload = done
      img.onerror = done
      img.src = url
    }
  }

  function draw() {
    if (!canvas || duration <= 0) return
    const dpr = window.devicePixelRatio || 1
    canvas.width = width * dpr
    canvas.height = HEIGHT * dpr
    const g = canvas.getContext('2d')!
    g.setTransform(dpr, 0, 0, dpr, 0, 0)
    g.clearRect(0, 0, width, HEIGHT)
    g.fillStyle = '#171515'
    g.fillRect(0, 0, width, HEIGHT)

    // Ruler.
    const step = niceStep((span() / width) * 110)
    g.fillStyle = '#b6b4ad'
    g.strokeStyle = '#46433f'
    g.font = '11px Segoe UI, sans-serif'
    for (let t = Math.ceil(viewStart / step) * step; t <= viewEnd; t += step) {
      const x = Math.round(toX(t)) + 0.5
      g.beginPath()
      g.moveTo(x, RULER - 6)
      g.lineTo(x, RULER)
      g.stroke()
      g.fillText(formatTime(t, step < 1).replace(/^00:/, ''), x + 3, RULER - 7)
    }

    // Filmstrip: thumbnails at a fixed grid so zooming reuses them.
    wanted = []
    const grid = niceStep((span() / width) * THUMB_W)
    for (let t = Math.floor(viewStart / grid) * grid; t < viewEnd; t += grid) {
      const img = thumb(Math.min(t + grid / 2, Math.max(0, duration - 0.5)))
      const x = toX(t)
      const w = toX(t + grid) - x
      if (img) {
        const iw = (img.naturalWidth / img.naturalHeight) * STRIP
        g.drawImage(img, x + (w - iw) / 2, RULER, iw, STRIP)
      } else {
        g.fillStyle = '#2a2828'
        g.fillRect(x + 1, RULER + 1, w - 2, STRIP - 2)
      }
    }

    // Nearest the playhead first.
    wanted.sort((a, b) => Math.abs(urlTime(a) - time) - Math.abs(urlTime(b) - time))
    pump()

    // Waveform.
    const mid = RULER + STRIP + WAVE / 2
    if (peaks && peaks.length) {
      g.fillStyle = '#5f8a2a'
      for (let x = 0; x < width; x++) {
        const i0 = Math.floor(toT(x) * peaksPerSecond)
        const i1 = Math.max(i0 + 1, Math.floor(toT(x + 1) * peaksPerSecond))
        let p = 0
        for (let i = i0; i < i1 && i < peaks.length; i++) p = Math.max(p, peaks[i])
        const v = Math.min(1, Math.max(0, (p - range.lo) / (range.hi - range.lo)))
        if (v > 0) {
          const h = Math.max(0.5, v * (WAVE / 2 - 3))
          g.fillRect(x, mid - h, 1, h * 2)
        }
      }
    }
    g.strokeStyle = '#2f2c2b'
    g.beginPath()
    g.moveTo(0, mid + 0.5)
    g.lineTo(width, mid + 0.5)
    g.stroke()

    // Shade what's cut away, and draw the handles.
    g.fillStyle = 'rgba(0,0,0,0.55)'
    if (start != null) g.fillRect(0, RULER, Math.max(0, toX(start)), HEIGHT - RULER)
    if (end != null) g.fillRect(toX(end), RULER, Math.max(0, width - toX(end)), HEIGHT - RULER)
    for (const [t, color, label] of [
      [start, '#78a22f', 'START'],
      [end, '#d9534f', 'END'],
    ] as const) {
      if (t == null) continue
      const x = Math.round(toX(t)) + 0.5
      g.strokeStyle = color
      g.lineWidth = 2
      g.beginPath()
      g.moveTo(x, RULER)
      g.lineTo(x, HEIGHT)
      g.stroke()
      g.lineWidth = 1
      g.fillStyle = color
      // Labels sit outside the selection, so close marks never overlap,
      // unless that would push one off the edge.
      const tw = g.measureText(label).width + 8
      let lx = label === 'START' ? x - tw : x
      if (lx < 0 || lx + tw > width) lx = label === 'START' ? x : x - tw
      g.fillRect(lx, RULER, tw, 14)
      g.fillStyle = '#111'
      g.fillText(label, lx + 4, RULER + 11)
    }

    // Hover and playhead.
    if (hover != null) {
      const x = Math.round(toX(hover)) + 0.5
      g.strokeStyle = 'rgba(255,255,255,0.25)'
      g.beginPath()
      g.moveTo(x, 0)
      g.lineTo(x, HEIGHT)
      g.stroke()
    }
    const px = Math.round(toX(time)) + 0.5
    g.strokeStyle = '#ffffff'
    g.lineWidth = 2
    g.beginPath()
    g.moveTo(px, 0)
    g.lineTo(px, HEIGHT)
    g.stroke()
    g.lineWidth = 1
  }

  const urlTime = (url: string) => parseFloat(new URL(url, location.href).searchParams.get('t') ?? '0')

  function nearHandle(x: number): 'start' | 'end' | null {
    if (end != null && Math.abs(toX(end) - x) < 7) return 'end'
    if (start != null && Math.abs(toX(start) - x) < 7) return 'start'
    return null
  }

  function localX(e: PointerEvent | WheelEvent) {
    return e.clientX - canvas.getBoundingClientRect().left
  }

  function down(e: PointerEvent) {
    canvas.setPointerCapture(e.pointerId)
    const x = localX(e)
    drag = nearHandle(x) ?? 'scrub'
    move(e)
  }

  function move(e: PointerEvent) {
    const x = localX(e)
    hover = toT(x)
    canvas.style.cursor = drag === 'start' || drag === 'end' || nearHandle(x) ? 'ew-resize' : 'pointer'
    if (!drag) return
    const t = toT(x)
    if (drag === 'start') onstart(end != null ? Math.min(t, end - 0.05) : t)
    else if (drag === 'end') onend(start != null ? Math.max(t, start + 0.05) : t)
    else onseek(t, true)
  }

  function up(e: PointerEvent) {
    if (drag === 'scrub') onseek(toT(localX(e)), false)
    drag = null
  }

  function wheel(e: WheelEvent) {
    e.preventDefault()
    const s = span()
    if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) {
      const d = ((e.deltaX || e.deltaY) / width) * s
      viewStart = Math.max(0, Math.min(duration - s, viewStart + d))
      viewEnd = viewStart + s
      return
    }
    const at = toT(localX(e))
    const ns = Math.min(duration, Math.max(2, s * (e.deltaY > 0 ? 1.25 : 0.8)))
    const f = (at - viewStart) / s
    viewStart = Math.max(0, Math.min(duration - ns, at - f * ns))
    viewEnd = viewStart + ns
  }

  export function zoom(factor: number, around = time) {
    const ns = Math.min(duration, Math.max(2, span() * factor))
    viewStart = Math.max(0, Math.min(duration - ns, around - ns / 2))
    viewEnd = viewStart + ns
  }

  export function show(t: number) {
    if (t < viewStart || t > viewEnd) {
      const s = span()
      viewStart = Math.max(0, Math.min(duration - s, t - s / 2))
      viewEnd = viewStart + s
    }
  }
</script>

<div class="timeline" bind:this={wrap}>
  <canvas
    bind:this={canvas}
    style="width:{width}px;height:{HEIGHT}px"
    onpointerdown={down}
    onpointermove={move}
    onpointerup={up}
    onpointerleave={() => (hover = null)}
    onwheel={wheel}
  ></canvas>
</div>

<style>
  .timeline { width: 100%; border: 1px solid var(--border); border-radius: 6px; overflow: hidden; user-select: none; }
  canvas { display: block; touch-action: none; }
</style>
