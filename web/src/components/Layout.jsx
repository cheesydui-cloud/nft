import { createContext, useContext, useState, useEffect, useCallback, useRef } from 'react'
import { NavLink, useNavigate, Navigate } from 'react-router-dom'
import { api } from '../lib/api'
import { resolvedDark, getStoredTheme, setStoredTheme } from '../lib/theme'
import { hasLocalURIs, hasLocalProxies } from '../lib/landing'
import { Loading } from './ui'
import { BrandMark } from './BrandMark'

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
  const [toasts, setToasts] = useState([])
  const idRef = useRef(0)
  const timersRef = useRef([])

  const refreshUser = useCallback(async () => {
    try {
      const data = await api.get('/me')
      setUser(data?.user ?? null)
      setPanelName(data?.panel_name || '')
      setVersion(data?.version || '')
      return data
    } catch {
      setUser(null)
      return null
    }
  }, [])

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

  return (
    <UserCtx.Provider value={{ user, setUser, panelName, version, refreshUser }}>
      <ToastCtx.Provider value={toast}>
        {children}
        {/* Toast stack */}
        <div className="fixed bottom-6 left-1/2 -translate-x-1/2 z-[100] flex flex-col gap-2 items-center pointer-events-none">
          {toasts.map(t => (
            <div key={t.id} className={`toast-item ${t.type === 'error' ? 'is-error' : t.type === 'info' ? 'is-info' : 'is-success'}`}>
              {t.type === 'error'
                ? <svg className="w-4 h-4 text-red-400 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><path d="M18 6L6 18M6 6l12 12"/></svg>
                : t.type === 'info'
                ? <svg className="w-4 h-4 text-blue-400 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/></svg>
                : <svg className="w-4 h-4 text-green-400 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><path d="M20 6L9 17l-5-5"/></svg>}
              <span className="leading-snug">{t.msg}</span>
            </div>
          ))}
        </div>
      </ToastCtx.Provider>
    </UserCtx.Provider>
  )
}

/* ---------- Layout (sidebar + content) ---------- */
export function Layout({ children }) {
  const { user, panelName, version } = useUser()
  const navigate = useNavigate()
  const [sideOpen, setSideOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem('nf-sidebar') === '1')
  const { blurred, toggleBlur } = useContext(BlurCtx)
  const { copyFmt, toggleCopyFmt } = useContext(CopyFmtCtx)
  const [theme, setThemeState] = useState(getStoredTheme())
  const isDark = resolvedDark(theme)

  useEffect(() => {
    if (panelName) document.title = panelName
  }, [panelName])

  // The landing-nodes entry shows when the user has an admin-assigned source or
  // their own browser-local URIs. Local URIs change in the same tab, which the
  // native 'storage' event misses, so re-check on our custom event too.
  const [, bumpLanding] = useState(0)
  useEffect(() => {
    const h = () => bumpLanding(t => t + 1)
    window.addEventListener('nf-landing-changed', h)
    window.addEventListener('storage', h)
    return () => { window.removeEventListener('nf-landing-changed', h); window.removeEventListener('storage', h) }
  }, [])

  const toggleCollapse = () => {
    setCollapsed(v => { localStorage.setItem('nf-sidebar', v ? '0' : '1'); return !v })
  }

  const toggleTheme = () => {
    const next = isDark ? 'light' : 'dark'
    setStoredTheme(next)
    setThemeState(next)
  }

  const handleLogout = async () => {
    try { await fetch('/api/logout', { method: 'POST' }) } catch {}
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

  return (
    <div className="flex h-screen overflow-hidden bg-app">
        {/* Mobile overlay */}
        {sideOpen && <div className="fixed inset-0 bg-black/30 z-30 lg:hidden" onClick={() => setSideOpen(false)} />}

        {/* Sidebar */}
        <aside className={`sb-aside fixed inset-y-0 left-0 z-40 flex flex-col transition-all lg:translate-x-0 lg:static lg:z-auto ${sideOpen ? 'translate-x-0 w-[248px]' : '-translate-x-full w-[248px]'} ${collapsed ? 'lg:w-[68px]' : 'lg:w-[248px]'}`}>

          {/* Brand */}
          <div className={`flex items-center gap-3 pt-5 pb-4 ${collapsed ? 'px-3 justify-center' : 'px-5'}`}>
            <div className="w-[42px] h-[42px] rounded-[13px] flex-none grid place-items-center text-white shadow-[0_10px_24px_-8px_rgba(79,70,229,0.7)] ring-1 ring-white/20"
              title={collapsed && version ? version : undefined}
              style={{ background: 'linear-gradient(145deg, #3b82f6 0%, #4f46e5 52%, #7c3aed 100%)' }}>
              <BrandMark />
            </div>
            {!collapsed && <div className="min-w-0">
              <div className="text-[15.5px] font-bold tracking-tight sb-text truncate">{panelName || 'nft'}</div>
              <div className="text-[11.5px] sb-text-mut mt-0.5">
                {isAdmin ? '管理面板' : '用户面板'}
                {version && <span className="font-mono"> · {version}</span>}
              </div>
            </div>}
          </div>

          {/* Nav */}
          <SidebarCtx.Provider value={collapsed}>
          <nav className={`flex-1 overflow-y-auto py-2 ${collapsed ? 'px-2' : 'px-4'}`}>
            {isAdmin ? (
              <>
                <NavGroup label="监控">
                  <SideLink to="/" icon={<IconDashboard />} end>运营概览</SideLink>
                </NavGroup>
                <NavGroup label="资源">
                  <SideLink to="/nodes" icon={<IconNodes />}>中转节点</SideLink>
                  <SideLink to="/node-repo" icon={<IconRepo />}>落地仓库</SideLink>
                  <SideLink to="/rules" icon={<IconForwards />}>转发规则</SideLink>
                  <SideLink to="/users" icon={<IconUserGroup />}>用户管理</SideLink>
                  {hasLocalProxies(user.username) && <SideLink to="/proxies" icon={<IconProxy />}>我的代理</SideLink>}
                </NavGroup>
                <NavGroup label="系统">
                  <SideLink to="/announcements" icon={<IconMegaphone />}>公告管理</SideLink>
                  <SideLink to="/docs" icon={<IconBook />}>使用文档</SideLink>
                  <SideLink to="/settings" icon={<IconSettings />}>系统设置</SideLink>
                </NavGroup>
              </>
            ) : (
              <>
                <NavGroup label="概况">
                  <SideLink to="/my" icon={<IconDashboard />} end>我的概览</SideLink>
                  <SideLink to="/my/docs" icon={<IconBook />}>使用文档</SideLink>
                </NavGroup>
                <NavGroup label="转发">
                  <SideLink to="/my/rules" icon={<IconForwards />}>我的规则</SideLink>
                  {(hasLocalProxies(user.username) || user.has_landing_source) && <SideLink to="/my/landing" icon={<IconProxy />}>落地节点</SideLink>}
                  {(hasLocalProxies(user.username) || user.has_landing_source) && <SideLink to="/proxies" icon={<IconProxy />}>我的代理</SideLink>}
                </NavGroup>
              </>
            )}
          </nav>
          </SidebarCtx.Provider>

          {/* Footer */}
          <div className={`sb-footer pt-3.5 ${collapsed ? 'p-2' : 'p-4'}`}>
            {collapsed ? (
              <div className="flex flex-col items-center gap-2">
                <div className="w-[34px] h-[34px] rounded-[9px] sb-avatar grid place-items-center font-bold text-[14px]" title={user.username}>
                  {user.username?.charAt(0).toUpperCase()}
                </div>
                <button onClick={handleLogout} title="退出登录" className="w-[34px] h-[34px] rounded-lg sb-btn transition-colors grid place-items-center">
                  <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/></svg>
                </button>
              </div>
            ) : (<>
              <div className="flex items-center gap-[11px] px-2 py-1.5 mb-3.5">
                <div className="w-[34px] h-[34px] rounded-[9px] sb-avatar grid place-items-center font-bold text-[14px] flex-none">
                  {user.username?.charAt(0).toUpperCase()}
                </div>
                <div className="min-w-0">
                  <div className="text-[13.5px] sb-text font-semibold leading-tight truncate">{user.username}</div>
                  <div className="text-[12px] sb-text-mut mt-px">{user.role}</div>
                </div>
              </div>
              <div className="flex gap-2">
                <NavLink to="/change-password" className="flex-1 text-center text-[12.5px] sb-btn py-2 rounded-lg transition-colors">账户设置</NavLink>
                <button onClick={handleLogout} className="flex-1 text-center text-[12.5px] sb-btn py-2 rounded-lg transition-colors">退出登录</button>
              </div>
            </>)}
            {/* Collapse toggle — desktop only */}
            <button onClick={toggleCollapse} title={collapsed ? '展开侧栏' : '收起侧栏'}
              className={`hidden lg:flex items-center justify-center w-full mt-2.5 py-1.5 rounded-lg sb-collapse transition-colors`}>
              <svg className={`w-4 h-4 transition-transform ${collapsed ? 'rotate-180' : ''}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m11 17-5-5 5-5"/><path d="m18 17-5-5 5-5"/></svg>
            </button>
          </div>
        </aside>

        {/* Content */}
        <main className="flex-1 min-w-0 flex flex-col">
          {/* Topbar */}
          <div className="app-topbar sticky top-0 z-20 h-[56px] flex-shrink-0 px-4 sm:px-7 flex items-center gap-2">
            <button onClick={() => setSideOpen(true)} className="lg:hidden p-1.5 rounded-lg text-ink-soft hover:text-ink hover:bg-raised transition-colors">
              <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M3 12h18M3 6h18M3 18h18"/></svg>
            </button>
            <div className="flex-1" />
            <div className="topbar-toggles" role="group" aria-label="显示选项">
              <button type="button" onClick={toggleTheme} title={isDark ? '切换到浅色' : '切换到深色'}
                className="topbar-toggle">
                {isDark ? (
                  <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>
                ) : (
                  <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/></svg>
                )}
                <span className="hidden sm:inline">{isDark ? '浅色' : '深色'}</span>
              </button>
              <button type="button" onClick={toggleCopyFmt} title="切换复制代理连接的格式（URI / YAML）"
                className={`topbar-toggle ${copyFmt === 'yaml' ? 'is-active' : ''}`}>
                <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M16 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V8Z"/><path d="M15 3v4a2 2 0 0 0 2 2h4"/></svg>
                {copyFmt === 'yaml' ? 'YAML' : 'URI'}
              </button>
              <button type="button" onClick={toggleBlur} title="模糊敏感信息"
                className={`topbar-toggle ${blurred ? 'is-active' : ''}`}>
                <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
                <span className="hidden sm:inline">脱敏</span>
              </button>
            </div>
          </div>

          {/* Page content */}
          <div className="flex-1 min-h-0 overflow-y-auto px-4 sm:px-7 py-7 pb-12">
            <div className="max-w-[1680px] mx-auto h-full">
              {children}
            </div>
          </div>
        </main>
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

/* ---------- Icons ---------- */
function IconDashboard() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="7" height="9" rx="1"/><rect x="14" y="3" width="7" height="5" rx="1"/><rect x="14" y="12" width="7" height="9" rx="1"/><rect x="3" y="16" width="7" height="5" rx="1"/></svg>
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
