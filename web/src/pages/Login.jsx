import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useUser } from '../components/Layout'
import { BrandMark } from '../components/BrandMark'

export default function Login() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [panelName, setPanelName] = useState('')
  const navigate = useNavigate()
  const { setUser } = useUser()

  useEffect(() => {
    api.get('/branding').then(d => setPanelName(d?.panel_name || '')).catch(() => {})
  }, [])

  const submit = async (e) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const data = await api.post('/login', { username, password })
      // Login API returns {user: {...}} — set user directly from the response
      // so that by the time navigate() triggers the route re-render, the
      // user state is already committed.  If we waited for a second /api/me
      // call instead, React state would still be pending when RootRedirect
      // reads it, causing it to see null and bounce back to /login.
      if (data?.user) {
        setUser(data.user)
      }
      navigate('/', { replace: true })
    } catch (err) {
      setError(err.message || '登录失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="login-shell">
      <div className="login-card">
        <div className="flex items-center gap-3.5 mb-8">
          <div className="w-[46px] h-[46px] rounded-[14px] grid place-items-center text-white shadow-[0_10px_28px_-8px_rgba(79,70,229,0.75)] ring-1 ring-white/25"
            style={{ background: 'linear-gradient(145deg, #3b82f6 0%, #4f46e5 52%, #7c3aed 100%)' }}>
            <BrandMark className="w-[28px] h-[28px]" />
          </div>
          <div>
            <div className="text-[17px] font-bold tracking-tight text-ink">{panelName || 'nft'}</div>
            <div className="text-[12.5px] text-ink-mut mt-0.5">登录以继续管理转发</div>
          </div>
        </div>

        {error && (
          <div className="mb-4 px-3.5 py-2.5 bg-rose-500/[.08] border border-rose-500/30 rounded-xl text-rose-600 dark:text-rose-300 text-[13px]">{error}</div>
        )}

        <form onSubmit={submit} className="flex flex-col gap-4">
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">用户名</label>
            <input className="input-field" value={username} onChange={e => setUsername(e.target.value)} required autoFocus autoComplete="username" />
          </div>
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">密码</label>
            <input className="input-field" type="password" value={password} onChange={e => setPassword(e.target.value)} required autoComplete="current-password" />
          </div>
          <button type="submit" disabled={loading}
            className="btn-primary mt-2 w-full h-11 justify-center text-[14px] disabled:opacity-60">
            {loading ? <div className="w-4 h-4 border-2 border-white border-t-transparent rounded-full animate-spin" /> : '登录'}
          </button>
        </form>
      </div>
    </div>
  )
}
