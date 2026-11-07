import { buildQuery, request } from './client'
import type {
  AppSettings,
  DashboardData,
  LoginResult,
  PageResult,
  ProxyUser,
  Rule,
  RuleInput,
  UserInput,
} from './types'

export const api = {
  login: (username: string, password: string) =>
    request<LoginResult>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),

  me: () => request<{ id: number; username: string }>('/auth/me'),

  changePassword: (oldPassword: string, newPassword: string) =>
    request<void>('/auth/password', {
      method: 'POST',
      body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
    }),

  dashboard: (range: '24h' | '7d' | '30d') =>
    request<DashboardData>(`/dashboard${buildQuery({ range })}`),

  rules: {
    list: (params: { page: number; page_size: number; keyword?: string }) =>
      request<PageResult<Rule>>(`/rules${buildQuery(params)}`),
    create: (payload: RuleInput) =>
      request<Rule>('/rules', { method: 'POST', body: JSON.stringify(payload) }),
    update: (id: number, payload: RuleInput) =>
      request<Rule>(`/rules/${id}`, { method: 'PUT', body: JSON.stringify(payload) }),
    setEnabled: (id: number, enabled: boolean) =>
      request<{ enabled: boolean }>(`/rules/${id}/enabled`, {
        method: 'PATCH',
        body: JSON.stringify({ enabled }),
      }),
    remove: (id: number) => request<void>(`/rules/${id}`, { method: 'DELETE' }),
  },

  users: {
    list: (params: { page: number; page_size: number; keyword?: string }) =>
      request<PageResult<ProxyUser>>(`/users${buildQuery(params)}`),
    create: (payload: UserInput) =>
      request<ProxyUser>('/users', { method: 'POST', body: JSON.stringify(payload) }),
    update: (id: number, payload: UserInput) =>
      request<ProxyUser>(`/users/${id}`, { method: 'PUT', body: JSON.stringify(payload) }),
    setEnabled: (id: number, enabled: boolean) =>
      request<{ enabled: boolean }>(`/users/${id}/enabled`, {
        method: 'PATCH',
        body: JSON.stringify({ enabled }),
      }),
    addQuota: (id: number, addBytes: number) =>
      request<ProxyUser>(`/users/${id}/quota`, {
        method: 'POST',
        body: JSON.stringify({ add_bytes: addBytes }),
      }),
    remove: (id: number) => request<void>(`/users/${id}`, { method: 'DELETE' }),
  },

  settings: {
    get: () => request<AppSettings>('/settings'),
  },
}
