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
  intro_duration: number
  outro: string
  outro_duration: number
  transition: string
  transition_duration: number
  hidden: boolean
}

export interface EncoderInfo {
  name: string
  presets: string[]
  defaultPreset: string
}

export interface Info {
  peaksPerSecond: number
  canOpenFiles: boolean
  encoders: EncoderInfo[]
}

export interface Slide {
  uid: string
  text: string
  at: string
}

export interface LiveState {
  obsConfigured: boolean
  obsConnected: boolean
  obsError: string
  recording: boolean
  elapsed: number
  ppConfigured: boolean
  ppConnected: boolean
  ppError: string
  slides: Slide[] | null
  watching: boolean
  phase: 'idle' | 'waiting' | 'recording' | 'stopped'
  jobId: string
  series: string
  notice: string
}

export interface SlideMatch {
  uid?: string
  text?: string
  match?: string
  case_sensitive?: boolean
}

export interface Settings {
  trimmed_dir: string
  final_dir: string
  pad_start: number
  pad_end: number
  render: Render
  backlogs: string[] | null
  obs: { host: string; port: number; password: string }
  propresenter: { host: string; port: number; password: string; begin_slide: SlideMatch; end_slide: SlideMatch }
  api: { enabled: boolean; host: string; port: number; token: string }
  hasOBSPassword?: boolean
  hasPPPassword?: boolean
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
  live: () => call<LiveState>('GET', '/api/live'),
  liveStart: (series: string) => call<LiveState>('POST', '/api/live/start', { series }),
  liveStop: () => call<LiveState>('POST', '/api/live/stop'),
  liveMark: (which: 'start' | 'end') => call<LiveState>('POST', '/api/live/mark', { which }),
  liveNudge: (which: 'start' | 'end', seconds: number) => call<LiveState>('POST', '/api/live/nudge', { which, seconds }),
  liveSeries: (name: string) => call<LiveState>('PUT', '/api/live/series', { name }),
  settings: () => call<Settings>('GET', '/api/settings'),
  saveSettings: (s: Settings & { clearOBSPassword?: boolean; clearPPPassword?: boolean }) =>
    call<{ settings: Settings; restartNeeded: boolean }>('PUT', '/api/settings', s),
  regenerateToken: () => call<{ restartNeeded: boolean }>('POST', '/api/settings/token'),
  dockURLs: () => call<{ urls: string[] }>('GET', '/api/settings/dock'),
  createSeries: (s: Series) => call<Series>('POST', '/api/series', s),
  updateSeries: (name: string, s: Series) => call<Series>('PUT', `/api/series/${encodeURIComponent(name)}`, s),
  deleteSeries: (name: string, force = false) =>
    call('DELETE', `/api/series/${encodeURIComponent(name)}${force ? '?force=1' : ''}`),
  importV1: () => call<{ folder: string; settings: boolean; series: number }>('POST', '/api/import/v1'),
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
