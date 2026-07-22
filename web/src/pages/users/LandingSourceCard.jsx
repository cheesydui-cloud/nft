import { useState, useEffect, useMemo } from 'react'
import { api } from '../../lib/api'
import { fmtTrafficGB, fmtDate, expiryBadge } from '../../lib/fmt'
import {
  fetchNodeRoles, nodeRoleKey, applyNodeRole, applyNodeRoleBatch, saveNodeRoles,
  ROLE_LANDING, ROLE_DIRECT,
} from '../../lib/landing'
import { useToast, useBlur } from '../../components/Layout'
import { Badge, Modal, SensText } from '../../components/ui'
import { TableBox } from '../../components/page'

const ADMIN_ROLE_OPTS = [
  [ROLE_LANDING, '落地', 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400 dark:border-emerald-700'],
  [ROLE_DIRECT, '直连', 'bg-blue-50 text-blue-700 border-blue-200 dark:bg-blue-900/30 dark:text-blue-400 dark:border-blue-700'],
]

function AdminRoleToggle({ state, onChange }) {
  return (
    <div className="inline-flex gap-1.5">
      {ADMIN_ROLE_OPTS.map(([bit, label, cls]) => (
        <button key={bit} type="button" onClick={() => onChange(bit)}
          className={`px-2 py-0.5 text-[11px] font-semibold rounded-md border transition-colors ${
            state & bit ? cls : 'bg-transparent border-line text-ink-mut/40 hover:text-ink-mut'
          }`}>
          {label}
        </button>
      ))}
    </div>
  )
}

function AdminRoleBulkToggle({ nodes, roleOf, onToggle }) {
  if (!nodes.length) return null
  return (
    <div className="flex gap-1.5 text-[12px]">
      {ADMIN_ROLE_OPTS.map(([bit, label, cls]) => {
        const allOn = nodes.every(n => roleOf(n) & bit)
        return (
          <button key={bit} type="button" onClick={() => onToggle(bit, !allOn)}
            className={`px-2 py-0.5 text-[11px] font-semibold rounded-md border transition-colors ${
              allOn ? cls : 'bg-transparent border-line text-ink-mut/40 hover:text-ink-mut'
            }`}>
            {label}
          </button>
        )
      })}
    </div>
  )
}

function PencilIcon({ className = '' }) {
  return (
    <svg className={className} width="12" height="12" viewBox="0 0 24 24" fill="none"
      stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </svg>
  )
}

function ExitNameCell({ userId, name, exit, onDone }) {
  const [editing, setEditing] = useState(false)
  const [val, setVal] = useState('')
  const toast = useToast()
  const effective = (exit?.name_override || name) || '(未命名)'
  if (!exit) return <span className="font-semibold">{effective}</span>
  const start = () => { setVal(exit.name_override || name || ''); setEditing(true) }
  const save = async () => {
    try {
      await api.post(`/users/${userId}/landing-exits/rename`, { host: exit.host, port: exit.port, name: val.trim() })
      toast(val.trim() ? '已改名' : '已恢复原名')
      setEditing(false)
      onDone()
    } catch (err) { toast(err.message, 'error') }
  }
  if (!editing) return (
    <button type="button" onClick={start}
      title={exit.name_override ? `原名称: ${name || '(未命名)'} · 点击改名` : '点击改名'}
      className="group/name inline-flex items-center gap-1.5 max-w-full text-left rounded-md -mx-1 px-1 py-0.5
        border border-transparent hover:border-emerald-500/40 hover:bg-emerald-500/[.06]
        focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/40 transition-colors">
      <span className="font-semibold text-ink group-hover/name:text-emerald-700 dark:group-hover/name:text-emerald-400 truncate">
        {effective}
      </span>
      {exit.name_override && (
        <span className="text-[10px] font-semibold text-emerald-600 dark:text-emerald-400 flex-none">已改</span>
      )}
      <span className="inline-flex items-center gap-0.5 flex-none text-ink-mut group-hover/name:text-emerald-600">
        <PencilIcon className="opacity-55 group-hover/name:opacity-100" />
        <span className="text-[11px] font-semibold opacity-70 group-hover/name:opacity-100">改名</span>
      </span>
    </button>
  )
  return (
    <form onSubmit={e => { e.preventDefault(); save() }} className="inline-flex items-center gap-1.5 flex-wrap">
      <input autoFocus className="input-field" value={val} onChange={e => setVal(e.target.value)}
        onKeyDown={e => { if (e.key === 'Escape') setEditing(false) }}
        placeholder="留空恢复原名" style={{ width: 140 }} />
      <button type="submit" className="btn-secondary text-xs">保存</button>
      <button type="button" className="text-xs text-ink-mut hover:text-ink" onClick={() => setEditing(false)}>取消</button>
    </form>
  )
}

export default function LandingSourceCard({ userId, subURL, uris, nodes, blurred: blurredProp, embedded = false }) {
  const blurred = blurredProp ?? useBlur()
  const [url, setUrl] = useState(subURL || '')
  const [text, setText] = useState(uris || '')
  const [preview, setPreview] = useState(nodes || [])
  const [roles, setRoles] = useState({})
  const [exits, setExits] = useState([])
  const [loading, setLoading] = useState(false)
  const [sel, setSel] = useState(new Set())
  const [showRepoPicker, setShowRepoPicker] = useState(false)
  const toast = useToast()

  useEffect(() => {
    api.get('/node-repo').catch(() => {})
    api.get('/users/' + userId + '/landing-exits')
      .then(d => setExits(d?.exits || []))
      .catch(err => toast(err.message, 'error'))
    fetchNodeRoles().then(setRoles).catch(() => setRoles({}))
  }, [userId])
  useEffect(() => { setSel(new Set()) }, [preview])

  const reloadLanding = () => {
    api.get(`/users/${userId}/landing-exits`)
      .then(d => {
        const ex = d?.exits || []
        setExits(ex)
        setPreview(ex.filter(e => e.present).map(e => ({
          host: e.host,
          port: e.port,
          name: e.name_override || e.name || '',
          // Keep source name so rename UI can restore / show original.
          source_name: e.name || '',
          protocol: e.protocol || '',
          expires_at: e.expires_at || 0,
        })))
      })
      .catch(err => toast(err.message, 'error'))
  }

  const submit = async (e) => {
    e.preventDefault()
    setLoading(true)
    try {
      await api.post(`/users/${userId}/landing`, { landing_sub_url: url.trim(), landing_uris: text })
      reloadLanding()
      toast('已保存')
    } catch (err) { toast(err.message, 'error') } finally { setLoading(false) }
  }

  const resetExit = async (ex) => {
    try {
      await api.post(`/users/${userId}/landing-exits/reset`, { host: ex.host, port: ex.port })
      toast('已重置'); reloadLanding()
    } catch (err) { toast(err.message, 'error') }
  }
  const deleteExit = async (ex) => {
    try {
      await api.post(`/users/${userId}/landing-exits/delete`, { host: ex.host, port: ex.port })
      toast('已删除'); reloadLanding()
    } catch (err) { toast(err.message, 'error') }
  }

  const handleSetRole = async (n, bit) => {
    const next = applyNodeRole(roles, n, bit)
    setRoles(next)
    try {
      await saveNodeRoles(next)
    } catch (err) {
      toast(err.message || '保存用途失败', 'error')
      fetchNodeRoles().then(setRoles).catch(() => {})
    }
  }
  const handleBulkRole = async (nodesList, bit, on) => {
    const next = applyNodeRoleBatch(roles, nodesList, bit, on)
    setRoles(next)
    try {
      await saveNodeRoles(next)
    } catch (err) {
      toast(err.message || '保存用途失败', 'error')
      fetchNodeRoles().then(setRoles).catch(() => {})
    }
  }
  const toggleSel = (i) => setSel(s => {
    const next = new Set(s)
    if (next.has(i)) next.delete(i); else next.add(i)
    return next
  })
  const toggleSelAll = () => setSel(s =>
    s.size === preview.length ? new Set() : new Set(preview.map((_, i) => i)))

  const roleOf = (n) => { const key = nodeRoleKey(n); return key ? (roles[key] || 0) : 0 }
  const exitByAddr = Object.fromEntries(exits.map(e => [`${e.host}:${e.port}`, e]))
  const residualExits = exits.filter(e => !e.present)
  const landingCount = preview.filter(n => roleOf(n) & ROLE_LANDING).length
  const directCount = preview.filter(n => roleOf(n) & ROLE_DIRECT).length
  const unconfiguredCount = preview.filter(n => !roleOf(n)).length
  // Keep quota UI visible when a ledger is already enforcing, even if the
  // landing mark is currently off.
  const showQuotaFor = (n, st) => {
    const ex = exitByAddr[`${n.host}:${n.port}`]
    if (!ex) return null
    return (st & ROLE_LANDING) || ex.quota_bytes > 0 || ex.used_bytes > 0 ? ex : null
  }
  const selectedNodes = preview.filter((_, i) => sel.has(i))

  const shellClass = embedded ? 'detail-panel' : 'card mb-5 soft-panel'

  return (
    <div className={shellClass}>
      <div className={embedded ? 'detail-panel-header' : 'card-header'}>
        <div className="min-w-0">
          <h3 className={embedded ? 'detail-panel-title' : 'text-sm font-bold'}>落地节点来源</h3>
          {embedded && (
            <div className="detail-panel-sub">
              {preview.length} 个节点 · {landingCount} 落地 · {directCount} 直连 · {unconfiguredCount} 未配置
            </div>
          )}
        </div>
        {!embedded && <span className="text-xs text-ink-mut">{preview.length} 个节点</span>}
      </div>
      <div className={embedded ? 'detail-panel-body space-y-4' : 'p-5'}>
        <form onSubmit={submit} className="space-y-3">
          <div>
            <label className="fl block mb-1.5">订阅地址 <span className="text-ink-mut font-normal text-xs">(可选，支持 Remnawave 等面板的订阅链接)</span></label>
            <input className={`input-field font-mono w-full ${blurred ? 'blur-[5px]' : ''}`} value={url} onChange={e => setUrl(e.target.value)}
              placeholder="https://example.com/api/sub/xxxx" />
          </div>
          <div>
            <label className="fl block mb-1.5">手动节点 URI <span className="text-ink-mut font-normal text-xs">(可选，每行一条，可与订阅组合)</span></label>
            <textarea className={`input-field font-mono w-full min-h-[120px] h-auto py-2.5 ${blurred ? 'blur-[5px]' : ''}`} rows={6} value={text} onChange={e => setText(e.target.value)}
              placeholder={'vless://…\ntrojan://…'} />
          </div>
          <div className="flex items-center gap-3">
            <button type="submit" disabled={loading} className="btn-primary text-xs">保存</button>
            <button type="button" onClick={() => setShowRepoPicker(true)} className="btn-secondary text-xs">从落地仓库导入</button>
          </div>
        </form>

        {showRepoPicker && (
          <RepoPicker
            userId={userId}
            existingExits={exits}
            onClose={() => setShowRepoPicker(false)}
            onDone={() => { setShowRepoPicker(false); reloadLanding() }}
          />
        )}

        {(preview.length > 0 || residualExits.length > 0) && (
          <div className="mt-4 border-t border-line-soft pt-4">
            <div className="flex items-center justify-between mb-2">
              <div className="text-xs font-bold text-ink-mut uppercase tracking-wider">
                已分配节点
                <span className="normal-case font-normal ml-2">{landingCount} 落地 · {directCount} 直连 · {unconfiguredCount} 未配置</span>
              </div>
              <AdminRoleBulkToggle nodes={selectedNodes} roleOf={roleOf}
                onToggle={(bit, on) => handleBulkRole(selectedNodes, bit, on)} />
            </div>
            <TableBox>
            <table className="tbl">
              <thead><tr>
                <th className="w-8"><input type="checkbox" className="accent-emerald-600"
                  checked={preview.length > 0 && sel.size === preview.length} onChange={toggleSelAll} /></th>
                <th>名称 <span className="font-normal text-ink-mut normal-case">（可改名）</span></th><th>协议</th><th>地址</th><th>限额</th><th>已用</th><th>到期时间</th><th className="text-right">用途</th><th className="text-right">操作</th></tr></thead>
              <tbody>
                {preview.map((n, i) => {
                  const st = roleOf(n)
                  const ex = showQuotaFor(n, st)
                  const exceeded = ex && ex.quota_bytes > 0 && ex.used_bytes >= ex.quota_bytes
                  return (
                    <tr key={i}>
                      <td><input type="checkbox" className="accent-emerald-600" checked={sel.has(i)} onChange={() => toggleSel(i)} /></td>
                      <td>
                        <ExitNameCell
                          userId={userId}
                          name={n.source_name || n.name}
                          exit={exitByAddr[`${n.host}:${n.port}`]}
                          onDone={reloadLanding}
                        />
                      </td>
                      <td className="font-mono text-xs text-ink-soft">{n.protocol}</td>
                      <td className="font-mono text-xs"><SensText blurred={blurred}>{n.host}:{n.port}</SensText></td>
                      <td>{ex ? <ExitQuotaForm userId={userId} exit={ex} onDone={reloadLanding} /> : <span className="text-xs text-ink-mut">—</span>}</td>
                      <td className="font-mono text-xs">
                        {ex ? (
                          <>
                            {fmtTrafficGB(ex.used_bytes, ex.quota_bytes)}
                            {exceeded && <Badge color="red">已超额</Badge>}
                            <button onClick={() => resetExit(ex)}
                              className="ml-2 px-2 py-0.5 text-[11px] font-semibold rounded-md border transition-colors bg-blue-50 text-blue-700 border-blue-200 hover:bg-blue-100 dark:bg-blue-900/30 dark:text-blue-400 dark:border-blue-700">
                              重置
                            </button>
                          </>
                        ) : <span className="text-ink-mut">—</span>}
                      </td>
                      <td>
                        <ExitExpiresForm userId={userId} host={n.host} port={n.port} exit={exitByAddr[`${n.host}:${n.port}`]} onDone={reloadLanding} />
                      </td>
                      <td className="text-right">
                        <AdminRoleToggle state={st} onChange={bit => handleSetRole(n, bit)} />
                      </td>
                      <td className="text-right">
                        <button onClick={() => deleteExit({ host: n.host, port: n.port })} className="text-red-600 text-xs font-semibold">删除</button>
                      </td>
                    </tr>
                  )
                })}
                {residualExits.map((ex, i) => {
                  const exceeded = ex.quota_bytes > 0 && ex.used_bytes >= ex.quota_bytes
                  return (
                    <tr key={`residual-${i}`} className="opacity-50">
                      <td></td>
                      <td>
                        <div className="flex items-center gap-1.5 flex-wrap">
                          <ExitNameCell userId={userId} name={ex.name} exit={ex} onDone={reloadLanding} />
                          <Badge color="gray">已不在来源</Badge>
                        </div>
                      </td>
                      <td className="font-mono text-xs text-ink-soft">{ex.protocol}</td>
                      <td className="font-mono text-xs"><SensText blurred={blurred}>{ex.host}:{ex.port}</SensText></td>
                      <td><ExitQuotaForm userId={userId} exit={ex} onDone={reloadLanding} /></td>
                      <td className="font-mono text-xs">
                        {fmtTrafficGB(ex.used_bytes, ex.quota_bytes)}
                        {exceeded && <Badge color="red">已超额</Badge>}
                        <button onClick={() => resetExit(ex)}
                              className="ml-2 px-2 py-0.5 text-[11px] font-semibold rounded-md border transition-colors bg-blue-50 text-blue-700 border-blue-200 hover:bg-blue-100 dark:bg-blue-900/30 dark:text-blue-400 dark:border-blue-700">
                              重置
                            </button>
                      </td>
                      <td>
                        <ExitExpiresForm userId={userId} host={ex.host} port={ex.port} exit={ex} onDone={reloadLanding} />
                      </td>
                      <td className="text-right"><span className="text-xs text-ink-mut">—</span></td>
                      <td className="text-right">
                        <button onClick={() => deleteExit(ex)} className="text-red-600 text-xs font-semibold">删除</button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
            </TableBox>
          </div>
        )}
      </div>
    </div>
  )
}

function ExitQuotaForm({ userId, exit, onDone }) {
  const [gb, setGb] = useState(String(Number(((exit.quota_bytes || 0) / 1073741824).toFixed(2))))
  const toast = useToast()
  const submit = async (e) => {
    e.preventDefault()
    const bytes = Math.max(0, Math.round((Number(gb) || 0) * 1073741824))
    try {
      await api.post(`/users/${userId}/landing-exits/quota`, { host: exit.host, port: exit.port, quota_bytes: bytes })
      toast('已设置')
      onDone()
    } catch (err) { toast(err.message, 'error') }
  }
  return (
    <form onSubmit={submit} className="inline-flex items-center gap-1.5">
      <input className="input-field font-mono" type="number" min="0" step="0.1" value={gb}
        onChange={e => setGb(e.target.value)} style={{ width: 80 }} title="0 = 不限" />
      <span className="text-xs text-ink-mut">GB</span>
      <button type="submit" className="btn-secondary text-xs">设限额</button>
    </form>
  )
}

function ExitExpiresForm({ userId, host, port, exit, onDone }) {
  const fmtDateInput = (ts) => {
    if (!ts || ts <= 0) return ''
    const d = new Date(ts * 1000)
    return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0')
  }
  const [val, setVal] = useState(fmtDateInput(exit?.expires_at))
  useEffect(() => { setVal(fmtDateInput(exit?.expires_at)) }, [exit?.expires_at])
  const [saving, setSaving] = useState(false)
  const toast = useToast()
  const submit = async (e) => {
    e.preventDefault()
    setSaving(true)
    try {
      let ts = 0
      if (val) {
        ts = Math.floor(new Date(val + 'T00:00:00').getTime() / 1000)
        if (isNaN(ts)) { toast('日期无效', 'error'); setSaving(false); return }
      }
      await api.post(`/users/${userId}/landing-exits/expires`, { host, port, expires_at: ts })
      toast(ts ? '已设置' : '已清除')
      onDone()
    } catch (err) { toast(err.message, 'error') } finally { setSaving(false) }
  }
  const expired = exit && exit.expires_at > 0 && exit.expires_at <= Math.floor(Date.now() / 1000)
  return (
    <form onSubmit={submit} className="inline-flex items-center gap-1">
      <input
        className="input-field font-mono text-[12px]"
        type="date"
        value={val}
        onChange={e => setVal(e.target.value)}
        onFocus={e => { try { e.currentTarget.showPicker?.() } catch {} }}
        style={{ width: 148, minWidth: 148 }}
      />
      <button type="submit" disabled={saving} className="btn-secondary text-[11px]">{saving ? '…' : '设'}</button>
      {expired && <Badge color="red">已过期</Badge>}
      {!expired && exit && exit.expires_at > 0 && (
        <button type="button" onClick={() => { setVal(''); submit({ preventDefault: () => {} }) }} className="text-[11px] text-ink-mut hover:text-red-600">清除</button>
      )}
    </form>
  )
}

function RepoPicker({ userId, existingExits = [], onClose, onDone }) {
  const [repoNodes, setRepoNodes] = useState([])
  const [folders, setFolders] = useState([])
  const [selected, setSelected] = useState(new Set())
  const [loading, setLoading] = useState(true)
  const [assigning, setAssigning] = useState(false)
  const [search, setSearch] = useState('')
  const [folderFilter, setFolderFilter] = useState('') // '' all | '0' ungrouped | folder id
  const toast = useToast()

  useEffect(() => {
    setLoading(true)
    Promise.all([
      api.get('/node-repo').then(d => setRepoNodes(d?.nodes || [])),
      api.get('/node-repo-folders').then(d => setFolders(d?.folders || [])).catch(() => setFolders([])),
    ]).catch(console.error).finally(() => setLoading(false))
  }, [])

  // host:port already on this user (any source) — still selectable for re-import/refresh.
  const existingAddr = useMemo(() => {
    const s = new Set()
    for (const e of existingExits) {
      if (e?.host && e?.port) s.add(`${e.host}:${e.port}`)
    }
    return s
  }, [existingExits])

  const ungroupedCount = useMemo(
    () => repoNodes.filter(n => !(n.group_id > 0)).length,
    [repoNodes],
  )

  const filtered = useMemo(() => {
    let list = repoNodes
    if (folderFilter === '0') list = list.filter(n => !(n.group_id > 0))
    else if (folderFilter) list = list.filter(n => String(n.group_id) === String(folderFilter))
    const q = search.trim().toLowerCase()
    if (q) {
      list = list.filter(n =>
        [n.name, n.protocol, `${n.host}:${n.port}`, n.remark, n.group_name]
          .some(v => (v || '').toLowerCase().includes(q)))
    }
    return list
  }, [repoNodes, folderFilter, search])

  const toggle = (id) => setSelected(s => {
    const next = new Set(s)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })

  const toggleAllFiltered = () => {
    const ids = filtered.map(n => n.id)
    setSelected(s => {
      const allOn = ids.length > 0 && ids.every(id => s.has(id))
      if (allOn) {
        const next = new Set(s)
        ids.forEach(id => next.delete(id))
        return next
      }
      const next = new Set(s)
      ids.forEach(id => next.add(id))
      return next
    })
  }

  const allFilteredSelected = filtered.length > 0 && filtered.every(n => selected.has(n.id))

  const assign = async () => {
    if (selected.size === 0) { toast('请至少选择一个节点', 'error'); return }
    setAssigning(true)
    try {
      const ids = [...selected]
      await api.post(`/users/${userId}/assign-repo`, { node_ids: ids })
      toast(`已分配 ${ids.length} 个节点`)
      onDone()
    } catch (err) { toast(err.message, 'error') } finally { setAssigning(false) }
  }

  const chip = (key, label, count) => {
    const active = folderFilter === key
    return (
      <button
        key={key || 'all'}
        type="button"
        onClick={() => setFolderFilter(key)}
        className={`inline-flex items-center gap-1.5 text-xs font-semibold px-2.5 py-1.5 rounded-lg border transition-colors whitespace-nowrap ${
          active
            ? 'bg-emerald-600 text-white border-emerald-600 shadow-sm'
            : 'bg-surface text-ink-soft border-line hover:bg-raised hover:text-ink'
        }`}
      >
        <span>{label}</span>
        <span className={`font-mono tabular-nums ${active ? 'text-white/80' : 'text-ink-mut'}`}>{count}</span>
      </button>
    )
  }

  return (
    <Modal open onClose={onClose} title="从落地仓库导入" wide>
      <div className="flex flex-col gap-3 min-h-0">
        {!loading && repoNodes.length > 0 && (
          <>
            <div className="flex flex-wrap items-center gap-2">
              {chip('', '全部', repoNodes.length)}
              {chip('0', '未分组', ungroupedCount)}
              {folders.map(f => chip(String(f.id), f.name, f.count ?? repoNodes.filter(n => String(n.group_id) === String(f.id)).length))}
            </div>
            <div className="flex items-center gap-2 flex-wrap">
              <input
                className="input-field flex-1 min-w-[160px] text-sm"
                value={search}
                onChange={e => setSearch(e.target.value)}
                placeholder="搜索名称、协议、地址、备注…"
              />
              <button
                type="button"
                onClick={toggleAllFiltered}
                disabled={filtered.length === 0}
                className="btn-secondary text-xs flex-none"
              >
                {allFilteredSelected ? '取消当前筛选' : `全选当前筛选 (${filtered.length})`}
              </button>
            </div>
            <div className="flex items-center justify-between text-xs text-ink-mut">
              <span>
                显示 {filtered.length} / {repoNodes.length}
                {selected.size > 0 && (
                  <span className="text-emerald-600 font-semibold ml-2">已选 {selected.size}</span>
                )}
              </span>
              {search || folderFilter ? (
                <button type="button" className="text-emerald-600 font-semibold hover:underline" onClick={() => { setSearch(''); setFolderFilter('') }}>
                  清除筛选
                </button>
              ) : null}
            </div>
          </>
        )}

        {loading ? (
          <div className="text-sm text-ink-mut text-center py-10">加载中…</div>
        ) : repoNodes.length === 0 ? (
          <div className="text-sm text-ink-mut text-center py-10">落地仓库为空，请先在「落地仓库」页面添加节点。</div>
        ) : filtered.length === 0 ? (
          <div className="text-sm text-ink-mut text-center py-10">无匹配节点，试试别的关键词或分组。</div>
        ) : (
          <div
            className="space-y-1 overflow-y-auto overscroll-contain border border-line-soft rounded-xl p-1.5"
            style={{ maxHeight: 'min(52vh, 440px)' }}
          >
            {filtered.map(n => {
              const addr = `${n.host}:${n.port}`
              const already = existingAddr.has(addr)
              const exp = n.expires_at > 0 ? expiryBadge(n.expires_at) : null
              return (
                <label
                  key={n.id}
                  className={`flex items-center gap-3 text-sm cursor-pointer py-2 px-3 rounded-lg hover:bg-raised transition-colors border ${
                    selected.has(n.id) ? 'border-emerald-500/40 bg-emerald-500/[.06]' : 'border-transparent hover:border-line'
                  }`}
                >
                  <input type="checkbox" className="accent-emerald-600 flex-none" checked={selected.has(n.id)} onChange={() => toggle(n.id)} />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-1.5 min-w-0">
                      <span className="font-semibold truncate">{n.name}</span>
                      {n.group_name ? <Badge color="blue" className="flex-none">{n.group_name}</Badge> : null}
                      {already ? <Badge color="gray" className="flex-none">已有</Badge> : null}
                      {exp ? <Badge color={exp.color} className="flex-none">{exp.label}</Badge> : null}
                    </div>
                    <div className="text-xs text-ink-mut font-mono truncate">
                      {n.protocol || '—'} · {addr}
                      {n.expires_at > 0 ? ` · 到期 ${fmtDate(n.expires_at)}` : ''}
                      {n.remark ? ` · ${n.remark}` : ''}
                    </div>
                  </div>
                </label>
              )
            })}
          </div>
        )}

        <div className="flex gap-2 pt-1 border-t border-line-soft">
          <button type="button" onClick={assign} disabled={assigning || selected.size === 0} className="btn-primary flex-1">
            {assigning ? '分配中…' : selected.size > 0 ? `分配选中 (${selected.size})` : '分配选中节点'}
          </button>
          <button type="button" onClick={onClose} className="btn-secondary flex-1">取消</button>
        </div>
      </div>
    </Modal>
  )
}
