import type { HostStatus } from '~/types/fleet'

type BadgeColor = 'success' | 'neutral' | 'info' | 'warning'

export const HOST_STATUS: Record<HostStatus, { label: string, color: BadgeColor }> = {
  online: { label: 'En ligne', color: 'success' },
  offline: { label: 'Hors ligne', color: 'neutral' },
  new: { label: 'Nouveau', color: 'info' },
  missing: { label: 'Disparu', color: 'warning' },
}

const PLATFORM_ICONS: Record<string, string> = {
  darwin: 'i-simple-icons-apple',
  ios: 'i-simple-icons-apple',
  ipados: 'i-simple-icons-apple',
  windows: 'i-simple-icons-windows',
  android: 'i-simple-icons-android',
  chrome: 'i-simple-icons-googlechrome',
}

export function platformIcon(platform: string): string {
  return PLATFORM_ICONS[platform] ?? 'i-simple-icons-linux'
}

const rtf = new Intl.RelativeTimeFormat('fr', { numeric: 'auto' })
const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 3600],
  ['month', 30 * 24 * 3600],
  ['day', 24 * 3600],
  ['hour', 3600],
  ['minute', 60],
]

// Fleet returns "0001-01-01T00:00:00Z" for hosts that were never seen.
export function timeAgo(iso: string | null | undefined): string {
  if (!iso || iso.startsWith('0001-'))
    return 'Jamais'
  const seconds = (new Date(iso).getTime() - Date.now()) / 1000
  for (const [unit, size] of UNITS) {
    if (Math.abs(seconds) >= size)
      return rtf.format(Math.round(seconds / size), unit)
  }
  return 'À l’instant'
}
