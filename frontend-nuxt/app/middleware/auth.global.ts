const PUBLIC_ROUTES = new Set(['/login'])

export default defineNuxtRouteMiddleware(async (to) => {
  const { isLoggedIn, user, fetchMe } = useAuth()

  if (PUBLIC_ROUTES.has(to.path)) {
    if (isLoggedIn.value)
      return navigateTo('/hosts')
    return
  }

  if (!isLoggedIn.value) {
    return navigateTo({ path: '/login', query: { redirect: to.fullPath } })
  }

  // Token restored from the cookie: load the user once to validate it.
  // An invalid token triggers a 401, which useFleetApi turns into a logout.
  if (!user.value) {
    try {
      await fetchMe()
    }
    catch {
      return navigateTo('/login')
    }
  }
})
