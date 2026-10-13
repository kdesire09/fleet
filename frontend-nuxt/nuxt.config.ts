// Fleet frontend POC on Nuxt 4 + Nuxt UI 4.
//
// The app runs as a pure SPA (no SSR): Fleet's API authenticates with a Bearer
// token kept in a cookie read client-side, so there is nothing to render server-side.
// In dev, `/api/**` is proxied to the Fleet server so the browser stays
// same-origin (no CORS) — mirroring how the legacy React UI is served.
import process from 'node:process'

const fleetApiUrl = process.env.FLEET_API_URL || 'https://localhost:8080'

export default defineNuxtConfig({
  modules: ['@nuxt/ui'],

  ssr: false,

  devtools: { enabled: true },

  css: ['~/assets/css/main.css'],

  app: {
    head: {
      title: 'Fleet',
      link: [{ rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    },
  },

  nitro: {
    devProxy: {
      '/api': {
        target: `${fleetApiUrl}/api`,
        changeOrigin: true,
        // The Fleet dev server uses a self-signed certificate.
        secure: false,
      },
    },
  },

  compatibilityDate: '2026-10-01',
})
