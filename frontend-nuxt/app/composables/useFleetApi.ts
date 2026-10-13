// Thin $fetch client for the Fleet REST API (/api/latest/fleet/*).
// Equivalent of frontend/services/index.ts in the legacy React app.
export function useFleetApi() {
  const { token, setSession } = useAuth()

  return $fetch.create({
    baseURL: '/api/latest/fleet',
    onRequest({ options }) {
      if (token.value) {
        options.headers.set('Authorization', `Bearer ${token.value}`)
      }
    },
    async onResponseError({ response }) {
      if (response.status === 401) {
        setSession(null)
        await navigateTo('/login')
      }
    },
  })
}

// Fleet errors look like { message, errors: [{ name, reason }] }.
export function fleetErrorMessage(err: unknown, fallback = 'Une erreur est survenue'): string {
  const data = (err as { data?: { message?: string, errors?: { reason: string }[] } })?.data
  return data?.errors?.[0]?.reason || data?.message || fallback
}
