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
    <div className="min-h-screen grid place-items-center px-4">
      <div className="bg-surface border border-line rounded-2xl p-9 w-full max-w-[400px] shadow-[0_24px_70px_-24px_rgba(15,23,42,0.45)]">
        <div className="flex items-center gap-3 mb-7">
          <div className="w-[42px] h-[42px] rounded-[11px] grid place-items-center text-white shadow-[0_8px_22px_-6px_rgba(79,70,229,0.75)]"
            style={{ background: 'linear-gradient(145deg, #3b82f6 0%, #4f46e5 52%, #7c3aed 100%)' }}>
            <BrandMark />
          </div>
          <div>
            <div className="text-[16px] font-bold tracking-wide text-ink">{panelName || 'nft'}</div>
            <div className="text-[12.5px] text-ink-mut mt-0.5">登录以继续</div>
          </div>
        </div>

        {error && (
          <div className="mb-4 px-3 py-2.5 bg-rose-500/[.08] border border-rose-500/30 rounded-lg text-rose-600 dark:text-rose-300 text-[13px]">{error}</div>
        )}

        <form onSubmit={submit} className="flex flex-col gap-3.5">
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">用户名</label>
            <input className="input-field" value={username} onChange={e => setUsername(e.target.value)} required autoFocus autoComplete="username" />
          </div>
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">密码</label>
            <input className="input-field" type="password" value={password} onChange={e => setPassword(e.target.value)} required autoComplete="current-password" />
          </div>
          <button type="submit" disabled={loading}
            className="btn-primary mt-3 w-full h-10 justify-center disabled:opacity-60">
            {loading ? <div className="w-4 h-4 border-2 border-white border-t-transparent rounded-full animate-spin" /> : '登录'}
          </button>
        </form>
      </div>
    </div>
  )
}
