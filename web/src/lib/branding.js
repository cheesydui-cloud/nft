/** Panel logo + favicon helpers. Custom logo is served from /api/branding/logo. */

const LOGO_KEY = 'nf-panel-logo-url'
const DEFAULT_FAVICON = '/favicon.svg'

/** Apply browser tab icon. Pass empty string to restore default. */
export function applyFavicon(url) {
  const href = (url || '').trim() || DEFAULT_FAVICON
  // Bust caches when rev query changes; keep type loose for png/svg/ico.
  const links = document.querySelectorAll("link[rel='icon'], link[rel='shortcut icon'], link[rel='apple-touch-icon']")
  if (links.length) {
    links.forEach(el => {
      el.setAttribute('href', href)
      if (href.includes('.svg') && !href.includes('branding/logo')) {
        el.setAttribute('type', 'image/svg+xml')
      } else {
        el.removeAttribute('type')
      }
    })
  } else {
    const link = document.createElement('link')
    link.rel = 'icon'
    link.href = href
    document.head.appendChild(link)
  }
  try {
    if (url && url.trim()) localStorage.setItem(LOGO_KEY, url.trim())
    else localStorage.removeItem(LOGO_KEY)
  } catch { /* ignore */ }
}

/** Early favicon from cache (call once on boot if needed). */
export function applyCachedFavicon() {
  try {
    const u = localStorage.getItem(LOGO_KEY)
    if (u && u.trim()) applyFavicon(u.trim())
  } catch { /* ignore */ }
}

export function logoURLFromBranding(d) {
  if (!d) return ''
  if (d.panel_logo_url) return d.panel_logo_url
  if (d.panel_logo && d.panel_logo_rev) return `/api/branding/logo?v=${d.panel_logo_rev}`
  if (d.panel_logo) return '/api/branding/logo'
  return ''
}
