import { reactive } from 'vue'

const TOKEN_KEY = 'cdnp-token'
const ADMIN_KEY = 'cdnp-admin'

export interface AdminSession {
  id: number
  username: string
}

function readAdmin(): AdminSession | null {
  try {
    const raw = localStorage.getItem(ADMIN_KEY)
    return raw ? (JSON.parse(raw) as AdminSession) : null
  } catch {
    return null
  }
}

export const auth = reactive<{ token: string; admin: AdminSession | null }>({
  token: localStorage.getItem(TOKEN_KEY) ?? '',
  admin: readAdmin(),
})

export function setSession(token: string, admin: AdminSession) {
  auth.token = token
  auth.admin = admin
  localStorage.setItem(TOKEN_KEY, token)
  localStorage.setItem(ADMIN_KEY, JSON.stringify(admin))
}

export function clearSession() {
  auth.token = ''
  auth.admin = null
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(ADMIN_KEY)
}

export function isLoggedIn(): boolean {
  return Boolean(auth.token)
}
