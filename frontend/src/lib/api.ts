// The app's HTTP API. Requests are same-origin and authorized by the
// cookie the server set when the window opened with its token.

export type Status = 'draft' | 'ready' | 'queued' | 'trimming' | 'trimmed' | 'stitching' | 'done' | 'failed'
export type Step = 'trim' | 'stitch'

export interface Render {
  trim_crf: number
  stitch_crf: number
  encoder: string
  preset: string
  fast_copy: boolean
  normalize: boolean
  target_lufs: number
  subsplash: boolean
}

export interface Job {
  id: string
  source: string
  recording: string
  start: number | null
  end: number | null
  date: string
  stem: string
  series: string
  trim_output?: string
  final_output?: string
  render: Render
  trimmed: string
  backlog?: string
  skipped?: boolean
  updated: string
  status: Status
  error: string
}

export interface BacklogSummary {
  dir: string
  total: number
  marked: number
  skipped: number
  missing: boolean
}

export interface Series {
  name: string
  intro: string
  outro: string
  transition: string
  transition_duration: number
  hidden: boolean
}

export interface Info {
  peaksPerSecond: number
  canOpenFiles: boolean
}

export interface Progress {
  id: string
  label: string
  fraction: number
  speed: string
}

export class ApiError extends Error {
  constructor(message: string, readonly problems?: Record<string, string[]>) {
    super(message)
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(data.error ?? `HTTP ${res.status}`, data.problems)
  return data as T
}

export const api = {
  info: () => call<Info>('GET', '/api/info'),
  jobs: () => call<Job[]>('GET', '/api/jobs'),
  series: () => call<Series[] | null>('GET', '/api/series'),
  createJob: (edit: Partial<Job>) => call<Job>('POST', '/api/jobs', edit),
  updateJob: (id: string, edit: Partial<Job>) => call<Job>('PATCH', `/api/jobs/${id}`, edit),
  deleteJob: (id: string) => call('DELETE', `/api/jobs/${id}`),
  editJobs: (ids: string[], edit: Partial<Job>) => call<Job[]>('POST', '/api/jobs/edit', { ids, edit }),
  importFile: (path: string) => call<{ imported: number }>('POST', '/api/import', { path }),
  render: (ids: string[], steps: Step[]) => call('POST', '/api/render', { ids, steps }),
  cancel: (id: string) => call('POST', `/api/jobs/${id}/cancel`),
  probe: (id: string) => call<{ duration: number; fps: number }>('GET', `/api/jobs/${id}/probe`),
  openFile: (title: string, patterns: string[]) =>
    call<{ path: string }>('POST', '/api/dialog/open', { title, patterns }),
  openFolder: (title: string) => call<{ path: string }>('POST', '/api/dialog/folder', { title }),
  backlogs: () => call<BacklogSummary[]>('GET', '/api/backlogs'),
  openBacklog: (dir: string) => call<BacklogSummary>('POST', '/api/backlogs', { dir }),
  forgetBacklog: (dir: string) => call('POST', '/api/backlogs/forget', { dir }),
  async peaks(id: string): Promise<{ peaks: Uint8Array; complete: boolean }> {
    const res = await fetch(`/api/jobs/${id}/peaks`)
    if (!res.ok) throw new ApiError(`waveform: HTTP ${res.status}`)
    return { peaks: new Uint8Array(await res.arrayBuffer()), complete: res.headers.get('X-Peaks-Complete') === 'true' }
  },
  videoURL: (id: string, trimmed = false) => `/api/jobs/${id}/video${trimmed ? '?file=trimmed' : ''}`,
  thumbURL: (id: string, t: number, h: number) => `/api/jobs/${id}/thumb?t=${t.toFixed(1)}&h=${h}`,
}
