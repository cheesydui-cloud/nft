import { useState } from 'react'
import { api } from '../lib/api'
import { Layout, useToast, useUser } from '../components/Layout'

export default function ChangePassword() {
  const [form, setForm] = useState({ old_password: '', new_password: '', confirm: '' })
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const toast = useToast()
  const { user } = useUser()

  const set = (k, v) => setForm(f => ({ ...f, [k]: v }))

  const submitPassword = async (e) => {
    e.preventDefault()
    setError('')
    if (form.new_password !== form.confirm) {
      setError('两次输入的密码不一致')
      return
    }
    if (form.new_password.length < 6) {
      setError('新密码至少 6 位')
      return
    }
    setLoading(true)
    try {
      await api.post('/change-password', { old_password: form.old_password, new_password: form.new_password })
      toast('密码已更新')
      setForm({ old_password: '', new_password: '', confirm: '' })
    } catch (err) { setError(err.message) } finally { setLoading(false) }
  }

  const rowClass = 'flex flex-col md:flex-row md:items-center gap-2 md:gap-6 mb-[22px]'
  const labelClass = 'w-[120px] flex-shrink-0 text-[14px] text-ink-soft'

  return (
    <Layout>
      <div className="card" style={{ maxWidth: 980 }}>
        <div className="card-header"><h3 className="text-[16px] font-bold">账户设置</h3></div>
        <div className="px-6 py-[26px]">
          <div className={`${rowClass} pb-[22px] border-b border-line-soft`}>
            <label className={labelClass}>用户名</label>
            <div className="text-[14.5px] font-semibold text-ink">{user?.username || '—'}</div>
          </div>

          <div className="pt-[26px]">
            <h4 className="text-[14px] font-semibold text-ink-soft mb-[18px]">修改密码</h4>
            {error && <div className="mb-4 px-3 py-2 bg-red-50 border border-red-200 rounded text-red-600 text-sm">{error}</div>}
            <form onSubmit={submitPassword}>
              <div className={rowClass}>
                <label className={labelClass}>原密码</label>
                <input className="input-field w-full md:max-w-[560px]" type="password" value={form.old_password} onChange={e => set('old_password', e.target.value)} required autoFocus />
              </div>
              <div className={rowClass}>
                <label className={labelClass}>新密码</label>
                <input className="input-field w-full md:max-w-[560px]" type="password" minLength="6" value={form.new_password} onChange={e => set('new_password', e.target.value)} required />
              </div>
              <div className="flex flex-col md:flex-row md:items-center gap-2 md:gap-6 pb-[22px] border-b border-line-soft">
                <label className={labelClass}>再次输入</label>
                <input className="input-field w-full md:max-w-[560px]" type="password" minLength="6" value={form.confirm} onChange={e => set('confirm', e.target.value)} required />
              </div>
              <div className="flex flex-col md:flex-row md:items-start gap-4 mt-[22px]">
                <button type="submit" disabled={loading} className="btn-primary w-full md:w-auto">更新密码</button>
                <span className="text-[13px] text-ink-mut md:leading-[38px]">提交后其他设备/浏览器上的旧会话会被注销，仅当前页保留。</span>
              </div>
            </form>
          </div>
        </div>
      </div>
    </Layout>
  )
}
