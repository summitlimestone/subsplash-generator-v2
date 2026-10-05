// Live app state, kept current by the server's push events.
import { api, type Info, type Job, type LiveState, type Progress, type Series } from './api'

export const app = $state({
  jobs: [] as Job[],
  series: [] as Series[],
  progress: {} as Record<string, Progress>,
  info: null as Info | null,
  live: null as LiveState | null,
  connected: false,
})

// peaksListeners are called when a recording's waveform grows.
const peaksListeners = new Set<(recording: string) => void>()
export function onPeaks(fn: (recording: string) => void): () => void {
  peaksListeners.add(fn)
  return () => peaksListeners.delete(fn)
}

function upsert(job: Job) {
  const i = app.jobs.findIndex((j) => j.id === job.id)
  if (i >= 0) app.jobs[i] = job
  else app.jobs.push(job)
  if (!['trimming', 'stitching'].includes(job.status)) delete app.progress[job.id]
}

export async function refresh() {
  const [jobs, series, info] = await Promise.all([api.jobs(), api.series(), api.info()])
  app.jobs = jobs ?? []
  app.series = series ?? []
  app.info = info
  app.live = await api.live().catch(() => null)
}

export function connect() {
  const es = new EventSource('/api/events')
  es.onopen = () => {
    app.connected = true
    // Catch up on anything missed while disconnected.
    refresh().catch(() => {})
  }
  es.onerror = () => (app.connected = false)
  es.addEventListener('job', (e) => upsert(JSON.parse((e as MessageEvent).data)))
  es.addEventListener('deleted', (e) => {
    const { id } = JSON.parse((e as MessageEvent).data)
    app.jobs = app.jobs.filter((j) => j.id !== id)
  })
  es.addEventListener('live', (e) => (app.live = JSON.parse((e as MessageEvent).data)))
  es.addEventListener('reload', () => refresh().catch(() => {}))
  es.addEventListener('progress', (e) => {
    const p: Progress = JSON.parse((e as MessageEvent).data)
    app.progress[p.id] = p
  })
  es.addEventListener('peaks', (e) => {
    const { recording } = JSON.parse((e as MessageEvent).data)
    peaksListeners.forEach((fn) => fn(recording))
  })
}

// isMarked reports whether a job has both marks.
export const isMarked = (j: Job) => j.start != null && j.end != null

// backlogOrder is the order a backlog's recordings are listed and marked in.
export function backlogJobs(dir: string): Job[] {
  return app.jobs
    .filter((j) => j.backlog === dir)
    .sort((a, b) => a.recording.toLowerCase().localeCompare(b.recording.toLowerCase()))
}

// nextToMark is the first unmarked, unskipped recording after `after`,
// wrapping around, or undefined when everything is done.
export function nextToMark(dir: string, after?: string): Job | undefined {
  const list = backlogJobs(dir)
  const i = after ? list.findIndex((j) => j.id === after) : -1
  const ordered = [...list.slice(i + 1), ...list.slice(0, i + 1)]
  return ordered.find((j) => !isMarked(j) && !j.skipped && j.id !== after)
}
