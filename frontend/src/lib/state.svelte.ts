// Live app state, kept current by the server's push events.
import { api, type Info, type Job, type Progress, type Series } from './api'

export const app = $state({
  jobs: [] as Job[],
  series: [] as Series[],
  progress: {} as Record<string, Progress>,
  info: null as Info | null,
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
  es.addEventListener('progress', (e) => {
    const p: Progress = JSON.parse((e as MessageEvent).data)
    app.progress[p.id] = p
  })
  es.addEventListener('peaks', (e) => {
    const { recording } = JSON.parse((e as MessageEvent).data)
    peaksListeners.forEach((fn) => fn(recording))
  })
}
