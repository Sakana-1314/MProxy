export type RuleTargetScheme = 'http' | 'https' | 'both'
export type RuleUpstreamScheme = 'http' | 'https' | 'follow'

export interface Rule {
  id: number
  name: string
  target_scheme: RuleTargetScheme
  match_host: string
  match_port: number
  upstream_host: string
  upstream_port: number
  upstream_scheme: RuleUpstreamScheme
  enabled: boolean
  remark: string
  created_at: number
  updated_at: number
}

export interface RuleInput {
  name: string
  target_scheme: RuleTargetScheme
  match_host: string
  match_port: number
  upstream_host: string
  upstream_port: number
  upstream_scheme: RuleUpstreamScheme
  enabled: boolean
  remark: string
}

export interface ProxyUser {
  id: number
  username: string
  enabled: boolean
  quota_bytes: number
  total_bytes: number
  expires_at: number | null
  note: string
  created_at: number
  updated_at: number
}

export interface UserInput {
  username: string
  password?: string
  enabled: boolean
  quota_bytes?: number
  expires_at: number | null
  note: string
}

export interface PageResult<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export interface SeriesPoint {
  ts: number
  requests: number
  bytes: number
}

export interface DashboardSummary {
  today_requests: number
  today_bytes: number
  total_users: number
  enabled_rules: number
}

export interface DashboardData {
  range: '24h' | '7d' | '30d'
  summary: DashboardSummary
  series: SeriesPoint[]
}

export interface AppSettings {
  proxy_listen: string
}

export interface LoginResult {
  token: string
  expires_at: number
  admin: { id: number; username: string }
}
