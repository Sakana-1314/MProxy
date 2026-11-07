const pad = (value: number) => String(value).padStart(2, '0')

const byteUnits = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

export function formatBytes(bytes: number | null | undefined, digits = 2): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes) || bytes <= 0) {
    return '0 B'
  }
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < byteUnits.length - 1) {
    value /= 1024
    unit += 1
  }
  const precision = value >= 100 ? 0 : value >= 10 ? Math.min(1, digits) : digits
  return `${value.toFixed(precision)} ${byteUnits[unit]}`
}

export function formatBytesShort(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes) || bytes <= 0) {
    return '0 B'
  }
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < byteUnits.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${Number(value.toFixed(1))} ${byteUnits[unit]}`
}

export function formatCount(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return '0'
  }
  return new Intl.NumberFormat('zh-CN').format(value)
}

export function formatDateTime(ts: number | null | undefined, withSeconds = true): string {
  if (!ts) {
    return '—'
  }
  const date = new Date(ts * 1000)
  const base = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(
    date.getHours(),
  )}:${pad(date.getMinutes())}`
  return withSeconds ? `${base}:${pad(date.getSeconds())}` : base
}

export function formatDate(ts: number | null | undefined): string {
  if (!ts) {
    return '—'
  }
  const date = new Date(ts * 1000)
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

export function formatHourLabel(ts: number): string {
  const date = new Date(ts * 1000)
  return `${pad(date.getHours())}:00`
}

export function formatDayLabel(ts: number): string {
  const date = new Date(ts * 1000)
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

export function formatDuration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || ms < 0) {
    return '—'
  }
  if (ms < 1000) {
    return `${Math.round(ms)} ms`
  }
  if (ms < 60_000) {
    return `${(ms / 1000).toFixed(2)} s`
  }
  const minutes = Math.floor(ms / 60_000)
  const seconds = Math.round((ms % 60_000) / 1000)
  return `${minutes}m ${seconds}s`
}

export function formatRelative(ts: number | null | undefined): string {
  if (!ts) {
    return '—'
  }
  const diff = Math.max(0, Math.floor(Date.now() / 1000) - ts)
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86_400) return `${Math.floor(diff / 3600)} 小时前`
  if (diff < 604_800) return `${Math.floor(diff / 86_400)} 天前`
  return formatDate(ts)
}

export function statusType(status: number): 'success' | 'info' | 'warning' | 'error' | 'default' {
  if (status >= 200 && status < 300) return 'success'
  if (status >= 300 && status < 400) return 'info'
  if (status >= 400 && status < 500) return 'warning'
  if (status >= 500) return 'error'
  return 'default'
}

export function usagePercent(used: number, quota: number): number {
  if (!quota || quota <= 0) {
    return 0
  }
  return Math.min(100, Math.max(0, (used / quota) * 100))
}

export function formatQuota(quota: number | null | undefined): string {
  if (!quota || quota <= 0) {
    return '不限'
  }
  return formatBytes(quota)
}

export function expiryText(ts: number | null | undefined): string {
  if (!ts) {
    return '永久'
  }
  if (ts * 1000 < Date.now()) {
    return '已过期'
  }
  return formatDate(ts)
}

export function endOfDaySeconds(ms: number): number {
  const date = new Date(ms)
  return Math.floor(new Date(date.getFullYear(), date.getMonth(), date.getDate(), 23, 59, 59).getTime() / 1000)
}

export function truncate(value: string, max = 60): string {
  if (value.length <= max) {
    return value
  }
  return `${value.slice(0, max - 1)}…`
}
