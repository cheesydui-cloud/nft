import { useEffect, useState } from 'react'
import { Modal } from './ui'
import { copyToClipboard } from '../lib/clipboard'
import { buildUserCardText, CARD_INITIAL_PASSWORD, loadPanelBranding } from '../lib/userCard'
import { api } from '../lib/api'

/**
 * Preview + copy admin user delivery card.
 * When rules are not passed, loads GET /users/:id for relay links.
 */
export function UserCardModal({ open, onClose, user, rules: rulesProp, toast }) {
  const [text, setText] = useState('')
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    if (!open || !user?.id) {
      setText('')
      setErr('')
      return
    }
    let cancelled = false
    setLoading(true)
    setErr('')
    ;(async () => {
      try {
        const { panelURL, panelName } = await loadPanelBranding(api)
        let rules = rulesProp
        let fullUser = user
        let expiryMap
        if (!rules) {
          const d = await api.get(`/users/${user.id}`)
          fullUser = d?.user || user
          rules = d?.rules || []
          expiryMap = new Map()
          for (const n of d?.landing_nodes || []) {
            if (n.expires_at > 0) expiryMap.set(`${n.host}:${n.port}`, n.expires_at)
          }
        }
        const card = buildUserCardText({
          panelName,
          panelURL,
          user: fullUser,
          rules: rules || [],
          expiryMap,
          password: CARD_INITIAL_PASSWORD,
        })
        if (!cancelled) setText(card)
      } catch (e) {
        if (!cancelled) setErr(e?.message || '加载失败')
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => { cancelled = true }
  }, [open, user?.id, rulesProp])

  const copy = async () => {
    if (!text) return
    try {
      await copyToClipboard(text)
      toast?.('名片已复制')
      onClose?.()
    } catch {
      toast?.('复制失败', 'error')
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="用户名片" wide>
      {loading && <p className="text-sm text-ink-mut">生成中…</p>}
      {err && <p className="text-sm text-red-600 mb-3">{err}</p>}
      {!loading && text && (
        <>
          <p className="text-[12px] text-ink-mut mb-3 m-0">
            初始密码固定为 <span className="font-mono font-semibold text-ink">{CARD_INITIAL_PASSWORD}</span>
            （与实际重置密码无关，便于统一交付文案）。
          </p>
          <pre className="text-[13px] font-mono whitespace-pre-wrap break-all bg-raised border border-line rounded-xl px-4 py-3 max-h-[50vh] overflow-y-auto text-ink leading-relaxed m-0">
            {text}
          </pre>
          <div className="flex justify-end gap-2 mt-5">
            <button type="button" className="btn-secondary text-xs" onClick={onClose}>关闭</button>
            <button type="button" className="btn-primary text-xs" onClick={copy}>复制名片</button>
          </div>
        </>
      )}
    </Modal>
  )
}
