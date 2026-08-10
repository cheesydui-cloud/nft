import { useCallback, useEffect, useMemo, useState } from 'react'
import { api } from '../../lib/api'
import { Layout, useToast, useUser } from '../../components/Layout'
import { Loading, Badge } from '../../components/ui'
import { PageHeader, Panel } from '../../components/page'
import { copyToClipboard } from '../../lib/clipboard'

const TOKEN_KEY = (u) => `nf-rule-sub-token:${u || ''}`

function loadStoredToken(username) {
  try {
    return localStorage.getItem(TOKEN_KEY(username)) || ''
  } catch {
    return ''
  }
}

function saveStoredToken(username, token) {
  try {
    if (token) localStorage.setItem(TOKEN_KEY(username), token)
    else localStorage.removeItem(TOKEN_KEY(username))
  } catch { /* ignore */ }
}

function buildUrls(base, token) {
  if (!token) return null
  const b = (base || '').replace(/\/$/, '')
  const root = b ? `${b}/sub/${token}` : `/sub/${token}`
  return {
    plain: root,
    mihomo: `${root}/mihomo.yaml`,
    global: `${root}/global.yaml`,
    sr: `${root}/shadowrocket.conf`,
  }
}

function downloadText(filename, text) {
  const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(a.href), 2000)
}

export default function MySubscription() {
  const toast = useToast()
  const { user } = useUser()
  const username = user?.username || ''

  const [loading, setLoading] = useState(true)
  const [data, setData] = useState(null)
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [previews, setPreviews] = useState({}) // kind -> content
  const [previewBusy, setPreviewBusy] = useState({})

  const base = data?.base_url || (typeof window !== 'undefined' ? window.location.origin : '')

  const urls = useMemo(() => {
    if (data?.urls) return data.urls
    return buildUrls(base, token)
  }, [data, base, token])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const d = await api.get('/my/subscription')
      setData(d)
      if (d?.token) {
        setToken(d.token)
        saveStoredToken(username, d.token)
      } else {
        const stored = loadStoredToken(username)
        if (stored) setToken(stored)
      }
    } catch (e) {
      toast(e.message || '加载失败', 'error')
    } finally {
      setLoading(false)
    }
  }, [toast, username])

  useEffect(() => { load() }, [load])

  const refreshToken = async () => {
    if (!confirm('重置后旧订阅链接立即失效，客户端需重新导入。确定？')) return
    setBusy(true)
    try {
      const d = await api.post('/my/subscription/refresh')
      if (d?.token) {
        setToken(d.token)
        saveStoredToken(username, d.token)
        setData(prev => ({
          ...(prev || {}),
          token: d.token,
          urls: d.urls,
          disabled: false,
          token_prefix: (d.token || '').slice(0, 8),
        }))
        toast('订阅已重置')
      }
    } catch (e) {
      toast(e.message || '重置失败', 'error')
    } finally {
      setBusy(false)
    }
  }

  const toggle = async () => {
    setBusy(true)
    try {
      const d = await api.post('/my/subscription/toggle')
      setData(prev => ({ ...(prev || {}), disabled: !!d?.disabled }))
      toast(d?.disabled ? '订阅已禁用' : '订阅已启用')
    } catch (e) {
      toast(e.message || '操作失败', 'error')
    } finally {
      setBusy(false)
    }
  }

  const copy = async (text, label) => {
    if (!text) {
      toast('暂无链接，请先重置订阅', 'error')
      return
    }
    try {
      await copyToClipboard(text)
      toast(label || '已复制')
    } catch {
      toast('复制失败', 'error')
    }
  }

  const loadPreview = async (kind) => {
    setPreviewBusy(p => ({ ...p, [kind]: true }))
    try {
      const d = await api.get(`/my/subscription/preview?kind=${encodeURIComponent(kind)}`)
      setPreviews(p => ({ ...p, [kind]: d?.content || '' }))
    } catch (e) {
      toast(e.message || '预览失败', 'error')
    } finally {
      setPreviewBusy(p => ({ ...p, [kind]: false }))
    }
  }

  const ensurePreview = async (kind) => {
    if (previews[kind]) return previews[kind]
    setPreviewBusy(p => ({ ...p, [kind]: true }))
    try {
      const d = await api.get(`/my/subscription/preview?kind=${encodeURIComponent(kind)}`)
      const c = d?.content || ''
      setPreviews(p => ({ ...p, [kind]: c }))
      return c
    } catch (e) {
      toast(e.message || '加载失败', 'error')
      return ''
    } finally {
      setPreviewBusy(p => ({ ...p, [kind]: false }))
    }
  }

  const downloadKind = async (kind, filename) => {
    const c = await ensurePreview(kind)
    if (c) downloadText(filename, c)
  }

  if (loading) {
    return (
      <Layout>
        <Loading />
      </Layout>
    )
  }

  const nodeCount = data?.node_count ?? 0
  const disabled = !!data?.disabled

  return (
    <Layout>
      <div className="space-y-4 pb-8">
        <PageHeader
          title="规则订阅"
          count={nodeCount}
          unit="节点"
          badge={disabled ? <Badge color="red">已禁用</Badge> : null}
          actions={
            <div className="flex flex-wrap gap-2">
              <button type="button" className="btn-secondary text-sm" disabled={busy} onClick={toggle}>
                {disabled ? '启用订阅' : '禁用订阅'}
              </button>
              <button type="button" className="btn-primary text-sm" disabled={busy} onClick={refreshToken}>
                重置订阅
              </button>
            </div>
          }
        />

        {nodeCount === 0 && (
          <div className="rounded-xl border border-amber-200 bg-amber-50 text-amber-900 dark:bg-amber-900/20 dark:text-amber-100 dark:border-amber-800 px-4 py-3 text-sm">
            暂无订阅节点。请联系管理员：发布代理服务（勾选「订阅可见」）并授权你线路节点后，节点会出现在这里。
          </div>
        )}

        {!urls && (
          <div className="rounded-xl border border-line bg-surface px-4 py-3 text-sm text-ink-soft">
            完整订阅链接仅在创建或重置时显示一次。请点右上角「重置订阅」生成新链接（会写入本机浏览器）。
          </div>
        )}

        {data?.nodes?.length > 0 && (
          <Panel>
            <div className="px-4 py-3 border-b border-line text-sm font-semibold">当前节点</div>
            <div className="px-4 py-3 flex flex-wrap gap-2">
              {data.nodes.map((n, i) => (
                <span key={i} className="inline-flex items-center gap-1.5 text-xs rounded-lg border border-line px-2.5 py-1 bg-surface">
                  <span className="font-mono text-ink-mut">{n.protocol}</span>
                  <span className="font-medium">{n.name}</span>
                  {n.host && <span className="text-ink-mut font-mono">{n.host}:{n.port}</span>}
                </span>
              ))}
            </div>
          </Panel>
        )}

        {/* Clash 分流 */}
        <SubCard
          title="Clash 分流订阅（国内直连）"
          hint="Clash Verge / Windows / Mac 分流用：国内站直连、国外走节点。链接须以 …/mihomo.yaml 结尾。INS/TK 防泄漏请用下方「全局防泄漏」。"
          url={urls?.mihomo}
          actions={
            <>
              <button type="button" className="btn-primary text-sm" onClick={() => copy(urls?.mihomo, 'Clash 分流链接已复制')}>复制 Clash 分流链接</button>
              <button type="button" className="btn-secondary text-sm" onClick={() => copy(urls?.mihomo, 'Mihomo URL 已复制')}>复制 Mihomo URL</button>
              <button type="button" className="btn-secondary text-sm" disabled={previewBusy.mihomo}
                onClick={() => loadPreview('mihomo')}>{previewBusy.mihomo ? '加载中…' : '预览 YAML'}</button>
              <button type="button" className="btn-secondary text-sm" onClick={() => downloadKind('mihomo', 'mihomo.yaml')}>下载 YAML</button>
            </>
          }
          preview={previews.mihomo}
        />

        {/* 全局防泄漏 */}
        <SubCard
          title="全局防泄漏（小火箭 / INS·TK）"
          hint="Clash 全局防泄漏 YAML：无国内直连；DNS 1.1.1.1/8.8.8.8 + respect-rules；关 IPv6。电脑 Clash Verge 用这个。"
          url={urls?.global}
          actions={
            <>
              <button type="button" className="btn-primary text-sm" onClick={() => copy(urls?.global, '全局链接已复制')}>复制全局链接</button>
              <button type="button" className="btn-secondary text-sm" disabled={previewBusy.global}
                onClick={async () => {
                  const c = await ensurePreview('global')
                  if (c) copy(c, '全局 YAML 已复制')
                }}>复制全局 YAML</button>
              <button type="button" className="btn-secondary text-sm" onClick={() => downloadKind('global', 'global.yaml')}>下载全局 YAML</button>
            </>
          }
          preview={previews.global}
          onNeedPreview={() => loadPreview('global')}
        />

        {/* 小火箭 */}
        <SubCard
          title="小火箭专用配置（全面防泄漏 .conf）"
          hint="原生 Shadowrocket 配置，用来替换 default.conf：无 GEOIP,CN 直连；DNS 强制 1.1.1.1/8.8.8.8；关 IPv6；仅局域网直连；FINAL 全部代理。导入：配置 → 从 URL 下载此链接。"
          url={urls?.sr}
          actions={
            <>
              <button type="button" className="btn-primary text-sm" onClick={() => copy(urls?.sr, '小火箭链接已复制')}>复制小火箭链接</button>
              <button type="button" className="btn-secondary text-sm" disabled={previewBusy.sr}
                onClick={async () => {
                  const c = await ensurePreview('sr')
                  if (c) copy(c, '.conf 文本已复制')
                }}>复制 .conf 文本</button>
              <button type="button" className="btn-secondary text-sm" onClick={() => downloadKind('sr', 'shadowrocket.conf')}>下载 .conf</button>
            </>
          }
          preview={previews.sr}
          onNeedPreview={() => loadPreview('sr')}
        />

        {/* 普通订阅 */}
        <SubCard
          title="普通订阅（节点 URI 列表）"
          hint="base64 节点列表，给小火箭原生订阅/扫码或其它客户端加节点。Clash 请用上方 YAML 链接。请勿公开转发；泄露可点「重置订阅」。"
          url={urls?.plain}
          actions={
            <>
              <button type="button" className="btn-primary text-sm" onClick={() => copy(urls?.plain, '订阅已复制')}>复制订阅</button>
              <button type="button" className="btn-secondary text-sm" disabled={previewBusy.plain}
                onClick={() => loadPreview('plain')}>{previewBusy.plain ? '加载中…' : '预览 base64'}</button>
            </>
          }
          preview={previews.plain}
        />
      </div>
    </Layout>
  )
}

function SubCard({ title, hint, url, actions, preview, onNeedPreview }) {
  useEffect(() => {
    // lazy: if preview slot empty and card visible with onNeedPreview — skip auto
  }, [])
  return (
    <Panel>
      <div className="px-4 py-3 border-b border-line flex flex-wrap items-center justify-between gap-2">
        <div className="text-[15px] font-bold text-ink">{title}</div>
        <div className="flex flex-wrap gap-2">{actions}</div>
      </div>
      <div className="px-4 py-3 space-y-3">
        {url ? (
          <div className="font-mono text-[13px] text-ink break-all select-all">{url}</div>
        ) : (
          <div className="text-sm text-ink-mut">链接不可用 — 请重置订阅</div>
        )}
        {hint && <p className="text-[12.5px] text-ink-mut m-0 leading-relaxed">{hint}</p>}
        {preview != null && preview !== '' && (
          <pre className="m-0 max-h-56 overflow-auto rounded-xl border border-line bg-slate-50 dark:bg-slate-900/40 px-3 py-2.5 text-[12px] font-mono text-ink-soft whitespace-pre-wrap break-all">
            {preview}
          </pre>
        )}
        {preview === undefined && onNeedPreview && (
          <button type="button" className="text-xs text-emerald-600 hover:underline" onClick={onNeedPreview}>
            显示预览
          </button>
        )}
      </div>
    </Panel>
  )
}
