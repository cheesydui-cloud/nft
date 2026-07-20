import { useState } from 'react'
import { api } from '../../lib/api'
import { fmtDateInput } from '../../lib/fmt'
import { useToast } from '../../components/Layout'

export default function UserConfigCard({ userId, expiresAt, maxForwards, quotaBytes, resetDays, adminNote, billingRate, speedLimitMBytes, onDone }) {
  const [form, setForm] = useState({
    expiresAt: expiresAt ? fmtDateInput(expiresAt) : '',
    maxForwards: String(maxForwards || 0),
    quotaGB: String(Number(((quotaBytes || 0) / 1073741824).toFixed(2))),
    resetDays: String(resetDays || 0),
    adminNote: adminNote,
    billingRate: String(billingRate ?? 1),
    speedLimitMBytes: String(speedLimitMBytes || 0),
  })
  const [saving, setSaving] = useState(false)
  const toast = useToast()

  const set = (key) => (e) => setForm(f => ({ ...f, [key]: e.target.value }))
  const initExpiry = expiresAt ? fmtDateInput(expiresAt) : ''

  const addDays = (days) => {
    const base = form.expiresAt ? new Date(form.expiresAt + 'T00:00:00') : new Date()
    base.setDate(base.getDate() + days)
    const y = base.getFullYear()
    const m = String(base.getMonth() + 1).padStart(2, '0')
    const d = String(base.getDate()).padStart(2, '0')
    setForm(f => ({ ...f, expiresAt: `${y}-${m}-${d}` }))
  }

  const submit = async (e) => {
    e.preventDefault()
    setSaving(true)
    try {
      await api.patch(`/users/${userId}/profile`, {
        expires_at: form.expiresAt || '',
        max_forwards: Math.max(0, Math.round(Number(form.maxForwards) || 0)),
        traffic_quota_gb: Math.max(0, Number(form.quotaGB) || 0),
        traffic_reset_days: Math.max(0, Math.round(Number(form.resetDays) || 0)),
        admin_note: form.adminNote,
        billing_rate: Math.max(0, Number(form.billingRate) || 1),
        speed_limit_mbytes: Math.max(0, Math.round(Number(form.speedLimitMBytes) || 0)),
      })
      toast('已保存')
      onDone()
    } catch (err) { toast(err.message, 'error') } finally { setSaving(false) }
  }

  return (
    <form onSubmit={submit}>
      <div className="grid grid-cols-[120px_1fr] gap-x-4 gap-y-3.5 items-center max-w-xl">
        <label className="fl">到期时间</label>
        <div>
          <input className="input-field font-mono" type="date" value={form.expiresAt} onChange={set('expiresAt')} />
          <div className="flex items-center gap-1.5 mt-1.5 flex-wrap">
            {[[1,'1天'],[7,'7天'],[30,'30天'],[365,'1年']].map(([d, l]) => (
              <button key={d} type="button" onClick={() => addDays(d)}
                className="text-[11px] px-2 py-0.5 rounded border border-line bg-surface text-ink-soft hover:border-emerald-500 hover:text-emerald-600 transition-colors cursor-pointer">+{l}</button>
            ))}
            {form.expiresAt !== initExpiry && (
              <button type="button" onClick={() => setForm(f => ({ ...f, expiresAt: initExpiry }))}
                className="text-[11px] px-2 py-0.5 rounded border border-line bg-surface text-ink-mut hover:text-ink-soft transition-colors cursor-pointer">还原</button>
            )}
          </div>
        </div>

        <label className="fl">规则配额</label>
        <input className="input-field font-mono" type="number" min="0" step="1" value={form.maxForwards} onChange={set('maxForwards')} title="0 = 不限" />

        <label className="fl">流量配额</label>
        <div className="flex items-center gap-1.5">
          <input className="input-field font-mono flex-1" type="number" min="0" step="0.1" value={form.quotaGB} onChange={set('quotaGB')} title="0 = 不限" />
          <span className="text-xs text-ink-mut">GB</span>
        </div>

        <label className="fl">重置周期</label>
        <div className="flex items-center gap-1.5">
          <input className="input-field font-mono flex-1" type="number" min="0" step="1" value={form.resetDays} onChange={set('resetDays')} title="0 = 永不重置" />
          <span className="text-xs text-ink-mut">天</span>
        </div>

        <label className="fl">计费倍率</label>
        <div className="flex items-center gap-1.5">
          <input className="input-field font-mono flex-1" type="number" min="0" step="0.1" value={form.billingRate} onChange={set('billingRate')} title="1.0 = 原价，<1 折扣，>1 加价" />
          <span className="text-xs text-ink-mut">×</span>
        </div>

        <label className="fl">全局限速</label>
        <div className="flex items-center gap-1.5">
          <input className="input-field font-mono flex-1" type="number" min="0" step="1" value={form.speedLimitMBytes} onChange={set('speedLimitMBytes')} title="0 = 不限；当节点授权限速为 0 时作为默认值" />
          <span className="text-xs text-ink-mut">Mbps</span>
        </div>

        <label className="fl">管理备注</label>
        <input className="input-field" value={form.adminNote} onChange={set('adminNote')} placeholder="管理备注" />
      </div>
      <button type="submit" disabled={saving} className="btn-primary text-xs mt-4">{saving ? '保存中…' : '保存'}</button>
    </form>
  )
}