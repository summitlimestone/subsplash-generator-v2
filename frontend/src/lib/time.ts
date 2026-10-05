// HH:MM:SS.mmm, matching the Go timestamp package.

export function formatTime(seconds: number | null | undefined, ms = true): string {
  if (seconds == null || !isFinite(seconds)) return '--:--:--'
  let total = Math.round(seconds * 1000)
  const sign = total < 0 ? '-' : ''
  total = Math.abs(total)
  const h = Math.floor(total / 3_600_000)
  const m = Math.floor((total % 3_600_000) / 60_000)
  const s = Math.floor((total % 60_000) / 1000)
  const frac = total % 1000
  const pad = (n: number, w = 2) => String(n).padStart(w, '0')
  return `${sign}${pad(h)}:${pad(m)}:${pad(s)}${ms ? '.' + pad(frac, 3) : ''}`
}

// parseTime accepts HH:MM:SS(.fff), MM:SS(.fff) or plain seconds.
export function parseTime(text: string): number | null {
  const t = text.trim()
  if (/^\d+(\.\d+)?$/.test(t)) return parseFloat(t)
  const m = /^(?:(\d+):)?([0-5]?\d):([0-5]\d(?:\.\d+)?)$/.exec(t)
  if (!m) return null
  return (m[1] ? parseInt(m[1]) * 3600 : 0) + parseInt(m[2]) * 60 + parseFloat(m[3])
}
