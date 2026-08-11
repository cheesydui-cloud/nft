import { createContext, useContext, useState, useEffect, useCallback, useRef } from 'react'
import { NavLink, Navigate } from 'react-router-dom'
import { api } from '../lib/api'
import { resolvedDark, getStoredTheme, setStoredTheme } from '../lib/theme'
import { hasLocalURIs, hasLocalProxies } from '../lib/landing'
import { Loading } from './ui'
import { BrandMark } from './BrandMark'
import { LoginAnnouncementModal, clearLoginAnnouncementSession } from './LoginAnnouncementModal'

/* ---------- User context ---------- */
const UserCtx = createContext(null)
const ToastCtx = createContext(() => {})

export function useUser() {
  const ctx = useContext(UserCtx)
  if (!ctx) throw new Error('useUser must be used within UserProvider')
  return ctx
}
export function useToast() { return useContext(ToastCtx) }

export function UserProvider({ children }) {
  const [user, setUser] = useState(undefined) // undefined = loading, null = not logged in
  const [panelName, setPanelName] = useState('')
  const [version, setVersion] = useState('')
  const [komariUrl, setKomariUrl] = useState('')
  const [toasts, setToasts] = useState([])
  const idRef = useRef(0)
  const timersRef = useRef([])

  const refreshUser = useCallback(async () => {
    try {
      const data = await api.get('/me')
      setUser(data?.user ?? null)
      setPanelName(data?.panel_name || '')
      setVersion(data?.version || '')
      setKomariUrl(data?.komari_url || '')
      return data
    } catch {
      setUser(null)
      return null
    }
  }, [])

  // Public branding so login + tab title match 系统设置里的面板名称 even before auth.
  useEffect(() => {
    api.get('/branding').then(d => {
      const name = (d?.panel_name || '').trim()
      if (name) setPanelName(name)
    }).catch(() => {})
  }, [])

  // Keep browser tab title in sync with panel_name globally (login + after login).
  useEffect(() => {
    if (panelName) document.title = panelName
  }, [panelName])

  useEffect(() => { refreshUser() }, [refreshUser])

  useEffect(() => {
    const handler = () => setUser(null)
    window.addEventListener('nf-unauthorized', handler)
    return () => window.removeEventListener('nf-unauthorized', handler)
  }, [])

  useEffect(() => () => {
    timersRef.current.forEach(clearTimeout)
    timersRef.current = []
  }, [])

  const toast = useCallback((msg, type) => {
    const id = ++idRef.current
    const kind = type || 'success'
    setToasts(prev => [...prev.slice(-4), { id, msg, type: kind }])
    // Errors stay a bit longer so operators can read them.
    const ms = kind === 'error' ? 3600 : 2400
    const timer = setTimeout(() => setToasts(prev => prev.filter(t => t.id !== id)), ms)
    timersRef.current.push(timer)
  }, [])

  // applySession patches user + branding from a login/me-shaped payload so the
  // sidebar brand is correct on the first paint after login (not stuck on "nft").
  const applySession = useCallback((data) => {
    if (!data) return
    if (data.user !== undefined) setUser(data.user)
    if (data.panel_name !== undefined) setPanelName(data.panel_name || '')
    if (data.version !== undefined) setVersion(data.version || '')
    if (data.komari_url !== undefined) setKomariUrl(data.komari_url || '')
  }, [])

  return (
    <UserCtx.Provider value={{ user, setUser, panelName, version, komariUrl, refreshUser, applySession }}>
      <ToastCtx.Provider value={toast}>
        {children}
        {/* Toast stack */}
        <div className="fixed bottom-6 left-1/2 -translate-x-1/2 z-[100] flex flex-col gap-2 items-center pointer-events-none">
          {toasts.map(t => (
            <div key={t.id} className={`toast-item ${t.type === 'error' ? 'is-error' : t.type === 'info' ? 'is-info' : 'is-success'}`}>
              {t.type === 'error'
                ? <svg className="w-4 h-4 text-red-400 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><path d="M18 6L6 18M6 6l12 12"/></svg>
                : t.type === 'info'
                ? <svg className="w-4 h-4 text-sky-400 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/></svg>
                : <svg className="w-4 h-4 text-green-400 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><path d="M20 6L9 17l-5-5"/></svg>}
              <span className="leading-snug">{t.msg}</span>
            </div>
          ))}
        </div>
      </ToastCtx.Provider>
    </UserCtx.Provider>
  )
}

/* ---------- Layout (妙妙屋 top-nav + content) ---------- */
export function Layout({ children }) {
  const { user, panelName, version, komariUrl } = useUser()
  const [menuOpen, setMenuOpen] = useState(false)
  const [userOpen, setUserOpen] = useState(false)
  const { blurred, toggleBlur } = useContext(BlurCtx)
  const { copyFmt, toggleCopyFmt } = useContext(CopyFmtCtx)
  const [theme, setThemeState] = useState(getStoredTheme())
  const isDark = resolvedDark(theme)
  const userMenuRef = useRef(null)

  const [, bumpLanding] = useState(0)
  useEffect(() => {
    const h = () => bumpLanding(t => t + 1)
    window.addEventListener('nf-landing-changed', h)
    window.addEventListener('storage', h)
    return () => { window.removeEventListener('nf-landing-changed', h); window.removeEventListener('storage', h) }
  }, [])

  useEffect(() => {
    if (!userOpen) return
    const onDoc = (e) => {
      if (userMenuRef.current && !userMenuRef.current.contains(e.target)) setUserOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [userOpen])

  const toggleTheme = () => {
    const next = isDark ? 'light' : 'dark'
    setStoredTheme(next)
    setThemeState(next)
  }

  const handleLogout = async () => {
    try { await fetch('/api/logout', { method: 'POST' }) } catch {}
    clearLoginAnnouncementSession()
    window.location.href = '/login'
  }

  if (user === undefined) {
    return (
      <div className="h-screen flex items-center justify-center bg-app">
        <Loading />
      </div>
    )
  }
  if (user === null) return <Navigate to="/login" replace />

  const isAdmin = user.role === 'admin'

  const adminNav = [
    { to: '/', end: true, label: '运营概览', icon: <IconDashboard /> },
    komariUrl ? { href: komariUrl, label: '服务监控', icon: <IconMonitor /> } : null,
    { to: '/nodes', label: '线路节点', icon: <IconNodes /> },
    { to: '/proxy-services', label: '代理服务', icon: <IconProxy /> },
    { to: '/node-repo', label: '落地仓库', icon: <IconRepo /> },
    { to: '/rules', label: '代理转发', icon: <IconForwards /> },
    { to: '/users', label: '用户管理', icon: <IconUserGroup /> },
    hasLocalProxies(user.username) ? { to: '/proxies', label: '我的代理', icon: <IconProxy /> } : null,
    { to: '/announcements', label: '公告管理', icon: <IconMegaphone /> },
    { to: '/docs', label: '使用文档', icon: <IconBook /> },
    { to: '/settings', label: '系统设置', icon: <IconSettings /> },
  ].filter(Boolean)

  const userNav = [
    { to: '/my', end: true, label: '我的概览', icon: <IconDashboard /> },
    { to: '/my/docs', label: '使用文档', icon: <IconBook /> },
    { to: '/my/rules', label: '代理转发', icon: <IconForwards /> },
    { to: '/my/subscription', label: '规则订阅', icon: <IconSub /> },
    (hasLocalProxies(user.username) || user.has_landing_source) ? { to: '/my/landing', label: '落地节点', icon: <IconProxy /> } : null,
    (hasLocalProxies(user.username) || user.has_landing_source) ? { to: '/proxies', label: '我的代理', icon: <IconProxy /> } : null,
  ].filter(Boolean)

  const navItems = isAdmin ? adminNav : userNav

  const renderPill = (item) => {
    if (item.href) {
      return (
        <a key={item.href} href={item.href} target="_blank" rel="noopener noreferrer" className="mmw-nav-pill">
          {item.icon}<span>{item.label}</span>
        </a>
      )
    }
    return (
      <NavLink
        key={item.to}
        to={item.to}
        end={item.end}
        onClick={() => setMenuOpen(false)}
        className={({ isActive }) => `mmw-nav-pill${isActive ? ' is-active' : ''}`}
      >
        {item.icon}<span>{item.label}</span>
      </NavLink>
    )
  }

  return (
    <div className="mmw-shell">
      <header className="mmw-topnav">
        <div className="mmw-topnav-inner">
          <NavLink to={isAdmin ? '/' : '/my'} className="mmw-brand" onClick={() => setMenuOpen(false)}>
            <div className="mmw-brand-mark"><BrandMark className="w-[20px] h-[20px]" /></div>
            <span className="mmw-brand-name">{panelName || 'nft'}</span>
          </NavLink>

          <nav className="mmw-nav-scroll" aria-label="主导航">
            {navItems.map(renderPill)}
          </nav>

          <div className="mmw-top-actions">
            <button type="button" className="mmw-icon-btn lg:hidden" onClick={() => setMenuOpen(v => !v)} title="菜单" aria-label="菜单">
              <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M3 12h18M3 6h18M3 18h18"/></svg>
            </button>
            <button type="button" className="mmw-icon-btn" onClick={toggleTheme} title={isDark ? '切换到浅色' : '切换到深色'}>
              {isDark ? (
                <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>
              ) : (
                <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/></svg>
              )}
            </button>
            <button type="button" className={`mmw-icon-btn ${copyFmt === 'yaml' ? 'is-active' : ''}`} onClick={toggleCopyFmt} title="复制格式 URI/YAML">
              <span className="text-[11px] font-bold">{copyFmt === 'yaml' ? 'YML' : 'URI'}</span>
            </button>
            <button type="button" className={`mmw-icon-btn ${blurred ? 'is-active' : ''}`} onClick={toggleBlur} title="模糊敏感信息">
              <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
            </button>

            <div className="relative" ref={userMenuRef}>
              <button type="button" className="mmw-user-chip" onClick={() => setUserOpen(v => !v)}>
                <span className="mmw-user-avatar">{user.username?.charAt(0).toUpperCase()}</span>
                <span className="hidden sm:inline max-w-[100px] truncate">{user.username}</span>
                {isAdmin && <span className="hidden md:inline text-[10px] font-bold uppercase tracking-wide opacity-60">ADMIN</span>}
              </button>
              {userOpen && (
                <div className="absolute right-0 mt-2 w-[220px] rounded-[12px] border border-line bg-surface shadow-[var(--shadow-float)] py-2 z-50">
                  <div className="px-3.5 py-2.5 border-b border-line-soft">
                    <div className="text-[13px] font-semibold text-ink truncate">{user.username}</div>
                    {isAdmin && version && <div className="text-[11px] text-ink-mut font-mono mt-0.5">{version}</div>}
                  </div>
                  <NavLink to="/change-password" onClick={() => setUserOpen(false)}
                    className="block px-3.5 py-2.5 text-[13px] font-medium text-ink-soft hover:bg-[var(--brand-soft)] hover:text-[var(--brand-to)] transition-colors">
                    账户设置
                  </NavLink>
                  <button type="button" onClick={handleLogout}
                    className="w-full text-left px-3.5 py-2.5 text-[13px] font-medium text-ink-soft hover:bg-[var(--brand-soft)] hover:text-[var(--brand-to)] transition-colors bg-transparent border-0 cursor-pointer">
                    退出登录
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>

        {/* Mobile nav drawer */}
        {menuOpen && (
          <div className="mmw-mobile-nav border-t border-line px-3 py-3 flex flex-wrap gap-2 bg-surface">
            {navItems.map(renderPill)}
          </div>
        )}
      </header>

      <main className="mmw-main">
        <div className="mmw-main-inner">
          {children}
        </div>
      </main>

      {!isAdmin && <LoginAnnouncementModal />}
    </div>
  )
}

/* ---------- Blur context ---------- */
/* The provider is mounted above the routes (App) so the topbar toggle inside
   Layout and the pages reading useBlur() share one state. When the provider
   sat inside Layout, every page rendered Layout as its own child, so the
   page's useBlur() resolved above the provider and always read the default —
   the toggle never reached the page content. */
const BlurCtx = createContext({ blurred: false, toggleBlur: () => {} })
export function useBlur() { return useContext(BlurCtx).blurred }

export function BlurProvider({ children }) {
  const [blurred, setBlurred] = useState(() => localStorage.getItem('nf-blur') === '1')
  const toggleBlur = useCallback(() => {
    setBlurred(v => {
      localStorage.setItem('nf-blur', v ? '0' : '1')
      return !v
    })
  }, [])
  return <BlurCtx.Provider value={{ blurred, toggleBlur }}>{children}</BlurCtx.Provider>
}

/* ---------- Copy-format context ---------- */
const CopyFmtCtx = createContext({ copyFmt: 'uri', toggleCopyFmt: () => {} })
export function useCopyFmt() { return useContext(CopyFmtCtx) }

export function CopyFmtProvider({ children }) {
  const [copyFmt, setCopyFmt] = useState(() => localStorage.getItem('nf-copy-fmt') || 'uri')
  const toggleCopyFmt = useCallback(() => {
    setCopyFmt(f => {
      const next = f === 'uri' ? 'yaml' : 'uri'
      localStorage.setItem('nf-copy-fmt', next)
      return next
    })
  }, [])
  return <CopyFmtCtx.Provider value={{ copyFmt, toggleCopyFmt }}>{children}</CopyFmtCtx.Provider>
}

/* ---------- Nav helpers ---------- */
const SidebarCtx = createContext(false)

function NavGroup({ label, children }) {
  const collapsed = useContext(SidebarCtx)
  return (
    <div className="mt-5 first:mt-1">
      {!collapsed && <div className="px-3 pb-2 text-[10.5px] font-semibold uppercase sb-group-label">{label}</div>}
      <div className="flex flex-col gap-0.5">{children}</div>
    </div>
  )
}

function SideLink({ to, icon, end, children }) {
  const collapsed = useContext(SidebarCtx)
  return (
    <NavLink to={to} end={end} title={collapsed ? children : undefined}
      className={({ isActive }) =>
        `flex items-center ${collapsed ? 'justify-center px-2' : 'gap-3 px-3'} py-[9px] rounded-xl text-[13.5px] font-medium transition-all relative border ${isActive
          ? 'sb-link-active'
          : 'sb-link'}`
      }>
      <span className="w-[18px] h-[18px] flex-none opacity-90">{icon}</span>
      {!collapsed && <span className="tracking-tight">{children}</span>}
    </NavLink>
  )
}

function SideExtLink({ href, icon, children }) {
  const collapsed = useContext(SidebarCtx)
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" title={collapsed ? children : undefined}
      className={`flex items-center ${collapsed ? 'justify-center px-2' : 'gap-3 px-3'} py-[9px] rounded-xl text-[13.5px] font-medium transition-all relative border sb-link`}>
      <span className="w-[18px] h-[18px] flex-none opacity-90">{icon}</span>
      {!collapsed && <span className="tracking-tight">{children}</span>}
    </a>
  )
}

/* ---------- Icons ---------- */
function IconDashboard() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="7" height="9" rx="1"/><rect x="14" y="3" width="7" height="5" rx="1"/><rect x="14" y="12" width="7" height="9" rx="1"/><rect x="3" y="16" width="7" height="5" rx="1"/></svg>
}
function IconMonitor() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/></svg>
}
function IconNodes() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="4" width="18" height="6" rx="1.5"/><rect x="3" y="14" width="18" height="6" rx="1.5"/><path d="M7 7h.01M7 17h.01"/></svg>
}
function IconUserGroup() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="9" cy="8" r="3.2"/><path d="M3.5 19a5.5 5.5 0 0 1 11 0"/><path d="M16 8.5a3 3 0 0 1 0 5.5M18 19a5 5 0 0 0-3-4.6"/></svg>
}
function IconForwards() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M4 12h12"/><path d="M13 7l5 5-5 5"/><path d="M20 5v14"/></svg>
}
function IconProxy() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><path d="M12 8v4"/><path d="M12 16h.01"/></svg>
}
function IconSettings() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
}
function IconMegaphone() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m3 11 18-5v12L3 14v-3z"/><path d="M11.6 16.8a3 3 0 1 1-5.8-1.6"/>
</svg>
}
function IconRepo() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v6c0 1.66 4.03 3 9 3s9-1.34 9-3V5"/><path d="M3 11v6c0 1.66 4.03 3 9 3s9-1.34 9-3v-6"/></svg>
}
function IconBook() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/></svg>
}
function IconSub() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M4 6h16"/><path d="M4 12h10"/><path d="M4 18h16"/><path d="M16 10l4 2-4 2"/></svg>
}
