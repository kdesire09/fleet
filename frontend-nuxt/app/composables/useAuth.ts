import type { FleetUser, LoginResponse } from '~/types/fleet'

// Same cookie as the legacy React UI (frontend/utilities/auth_token/index.ts),
// so a session opened in one UI is reused by the other on the same origin.
// `__Host-` names require a secure context, hence the HTTP fallback.
const DEFAULT_EXPIRATION_DAYS = 5

function tokenCookie() {
  const isSecure = import.meta.client && window.location.protocol === 'https:'
  return useCookie<string | null>(isSecure ? '__Host-token' : 'token', {
    secure: isSecure,
    sameSite: 'lax',
    path: '/',
    maxAge: DEFAULT_EXPIRATION_DAYS * 24 * 60 * 60,
    default: () => null,
  })
}

export function useAuth() {
  const token = tokenCookie()
  const user = useState<FleetUser | null>('auth:user', () => null)

  const isLoggedIn = computed(() => !!token.value)

  function setSession(
    newToken: string | null,
    newUser: FleetUser | null = null,
  ) {
    token.value = newToken
    user.value = newUser
  }

  async function login(email: string, password: string) {
    // Plain $fetch: no Authorization header and no 401 redirect on the login call.
    const res = await $fetch.raw<LoginResponse>('/api/latest/fleet/login', {
      method: 'POST',
      body: { email, password, supports_email_verification: true },
    })

    // 202 = Fleet sent an email verification link (MFA). Not handled by the POC.
    if (res.status === 202 || !res._data?.token) {
      throw new Error('MFA_REQUIRED')
    }

    setSession(res._data.token, res._data.user)
  }

  async function fetchMe() {
    const res = await useFleetApi()<{ user: FleetUser }>('/me')
    user.value = res.user
    return res.user
  }

  async function logout() {
    try {
      await useFleetApi()('/logout', { method: 'POST' })
    }
    catch {
      // Token may already be invalid; we clear it locally regardless.
    }
    setSession(null)
    await navigateTo('/login')
  }

  return { token, user, isLoggedIn, login, logout, fetchMe, setSession }
}
