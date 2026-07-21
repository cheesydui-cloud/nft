import { useEffect, useRef, useState } from 'react'
import { api } from '../lib/api'
import { useUser } from './Layout'
import { Badge } from './ui'
import { fmtDate } from '../lib/fmt'

const AUTO_CLOSE_SEC = 30
const SESSION_KEY = 'nf-login-ann-shown'

const colorBadge = {
  red: { color: 'red', label: '紧急' },
  amber: { color: 'amber', label: '重要' },
  blue: { color: 'blue', label: '信息' },
  green: { color: 'green', label: '成功' },
}

const accentBar = {
  red: 'bg-rose-500',
  amber: 'bg-amber-500',
  blue: 'bg-sky-500',
  green: 'bg-emerald-500',
  default: 'bg-emerald-500',
}

// LoginAnnouncementModal shows the admin-designated notice once per user
// login session. Manual close or AUTO_CLOSE_SEC auto-dismiss. Admins never see it.
export function LoginAnnouncementModal() {
  const { user } = useUser()
  const [ann, setAnn] = useState(null)
  const [open, setOpen] = useState(false)
  const [left, setLeft] = useState(AUTO_CLOSE_SEC)
  const timerRef = useRef(null)
  const tickRef = useRef(null)
  const shownForUser = useRef(null)

  useEffect(() => {
    if (!user || user.role === 'admin') {
      setOpen(false)
      setAnn(null)
      return
    }
    // Once per browser session per user id (sessionStorage survives reloads
    // within the same tab session, but clears on full logout→login because
    // Login clears it; also guard by user id within this mount).
    const sessionFlag = sessionStorage.getItem(SESSION_KEY)
    if (sessionFlag === String(user.id) || shownForUser.current === user.id) {
      return
    }
    let cancelled = false
    api.get('/my/login-announcement').then(d => {
      if (cancelled) return
      const a = d?.announcement
      if (!a) return
      shownForUser.current = user.id
      sessionStorage.setItem(SESSION_KEY, String(user.id))
      setAnn(a)
      setLeft(AUTO_CLOSE_SEC)
      setOpen(true)
    }).catch(() => {})
    return () => { cancelled = true }
  }, [user])

  useEffect(() => {
    if (!open) {
      if (timerRef.current) clearTimeout(timerRef.current)
      if (tickRef.current) clearInterval(tickRef.current)
      return
    }
    setLeft(AUTO_CLOSE_SEC)
    tickRef.current = setInterval(() => {
      setLeft(v => (v > 0 ? v - 1 : 0))
    }, 1000)
    timerRef.current = setTimeout(() => setOpen(false), AUTO_CLOSE_SEC * 1000)
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
      if (tickRef.current) clearInterval(tickRef.current)
    }
  }, [open, ann?.id])

  if (!open || !ann) return null

  const meta = colorBadge[ann.color] || null
  const bar = accentBar[ann.color] || accentBar.default

  return (
    <div className="fixed inset-0 z-[80] flex items-center justify-center bg-black/50 backdrop-blur-[4px] px-4">
      <div
        className="bg-surface/95 border border-line rounded-[20px] shadow-[0_28px_80px_-24px_rgba(15,23,42,0.55)] w-full max-w-lg animate-in backdrop-blur-xl overflow-hidden"
        role="dialog"
        aria-modal="true"
        aria-label="登录公告"
      >
        <div className={`h-1.5 w-full ${bar}`} />
        <div className="flex items-start justify-between gap-3 px-6 py-4 border-b border-line-soft">
          <div className="min-w-0">
            <div className="flex items-center gap-2 flex-wrap mb-1">
              <Badge color="violet">登录公告</Badge>
              {meta && <Badge color={meta.color}>{meta.label}</Badge>}
              {(ann.pinned === 1 || ann.pinned === true) && <Badge color="amber">置顶</Badge>}
            </div>
            <h3 className="text-[17px] font-bold tracking-tight text-ink break-words">{ann.title}</h3>
          </div>
          <button
            type="button"
            onClick={() => setOpen(false)}
            className="w-8 h-8 rounded-lg text-ink-mut hover:text-ink hover:bg-raised transition-colors grid place-items-center text-lg leading-none flex-none"
            aria-label="关闭公告"
          >
            &times;
          </button>
        </div>
        <div className="px-6 py-5">
          <div className="text-[14px] text-ink-soft whitespace-pre-wrap leading-relaxed max-h-[50vh] overflow-y-auto">
            {ann.content}
          </div>
          {ann.created_at > 0 && (
            <div className="text-[11px] text-ink-mut mt-4">{fmtDate(ann.created_at)}</div>
          )}
        </div>
        <div className="px-6 pb-5 flex items-center justify-between gap-3">
          <span className="text-xs text-ink-mut">{left > 0 ? `${left} 秒后自动关闭` : '即将关闭…'}</span>
          <button type="button" onClick={() => setOpen(false)} className="btn-primary px-5">
            我知道了
          </button>
        </div>
      </div>
    </div>
  )
}

// Clear the per-session "already shown" flag so the next login can pop again.
export function clearLoginAnnouncementSession() {
  try { sessionStorage.removeItem(SESSION_KEY) } catch {}
}
