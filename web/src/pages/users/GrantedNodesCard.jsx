import { useState, useEffect, useMemo } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../../lib/api'
import { fmtTrafficGB } from '../../lib/fmt'
import { useToast } from '../../components/Layout'
import { useConfirm } from '../../components/ui'
import { Badge, NodeTypeBadge, Select, Empty } from '../../components/ui'
import { TableBox } from '../../components/page'
import { copyToClipboard } from '../../lib/clipboard'
import PasteGrantsModal from './PasteGrantsModal'

function PerNodeMaxForwardsForm({ userId, nodeId, maxForwards, onDone }) {
  const [val, setVal] = useState(String(maxForwards ?? 10))
  const toast = useToast()
  const submit = async (e) => {
    e.preventDefault()
    const n = Math.max(1, Number(val) || 1)
    try {
      await api.post(`/users/${userId}/nodes/${nodeId}/max-forwards`, { max_forwards: n })
      toast('已设置')
      onDone()
    } catch (err) { toast(err.message, 'error') }
  }
  return (
    <form onSubmit={submit} className="inline-flex items-center gap-1">
      <input className="input-field font-mono !h-8 !text-xs !px-2" type="number" min="1" value={val}
        onChange={e => setVal(e.target.value)} style={{ width: 56 }} />
      <button type="submit" className="btn-secondary !h-8 !px-2.5 text-[11px]">设</button>
    </form>
  )
}

function PerNodeQuotaForm({ userId, nodeId, quotaBytes, onDone }) {
  const [gb, setGb] = useState(String(Number(((quotaBytes || 0) / 1073741824).toFixed(2))))
  const toast = useToast()
  const submit = async (e) => {
    e.preventDefault()
    const bytes = Math.max(0, Math.round((Number(gb) || 0) * 1073741824))
    try {
      await api.post(`/users/${userId}/nodes/${nodeId}/quota`, { traffic_quota_bytes: bytes })
      toast('已设置')
      onDone()
    } catch (err) { toast(err.message, 'error') }
  }
  return (
    <form onSubmit={submit} className="inline-flex items-center gap-1">
      <input className="input-field font-mono !h-8 !text-xs !px-2" type="number" min="0" step="0.1" value={gb}
        onChange={e => setGb(e.target.value)} style={{ width: 64 }} title="0 = 不限" />
      <span className="text-[11px] text-ink-mut">GB</span>
      <button type="submit" className="btn-secondary !h-8 !px-2.5 text-[11px]">设</button>
    </form>
  )
}

function PerNodeRateForm({ userId, nodeId, rateMBytes, onDone }) {
  const [mb, setMb] = useState(String(rateMBytes || 0))
  const toast = useToast()
  const submit = async (e) => {
    e.preventDefault()
    const n = Math.max(0, Math.round(Number(mb) || 0))
    try {
      await api.post(`/users/${userId}/nodes/${nodeId}/rate-limit`, { rate_limit_mbytes: n })
      toast('已设置')
      onDone()
    } catch (err) { toast(err.message, 'error') }
  }
  return (
    <form onSubmit={submit} className="inline-flex items-center gap-1">
      <input className="input-field font-mono !h-8 !text-xs !px-2" type="number" min="0" value={mb}
        onChange={e => setMb(e.target.value)} style={{ width: 56 }} title="0 = 不限，同节点所有规则共享" />
      <span className="text-[11px] text-ink-mut">Mbps</span>
      <button type="submit" className="btn-secondary !h-8 !px-2.5 text-[11px]">设</button>
    </form>
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

const ADMIN_ROLE_OPTS = [
  [1, '落地', 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400 dark:border-emerald-700'],
  [2, '直连', 'bg-blue-50 text-blue-700 border-blue-200 dark:bg-blue-900/30 dark:text-blue-400 dark:border-blue-700'],
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

const PROTO_LABEL = {
  vless: 'VLESS',
  shadowsocks: 'Shadowsocks',
  mieru: 'mieru',
  socks5: 'SOCKS5',
  socks: 'SOCKS5',
  anytls: 'AnyTLS',
  naive: 'Naive',
  naiveproxy: 'Naive',
}

function GrantNodeForm({ userId, allNodes, grantedNodes, proxyNodeIds, proxyServices: proxyServicesProp, grantedProxyServiceIds = [], onDone }) {
  const [selected, setSelected] = useState([]) // string values: node id or "svc:<id>"
  const [max, setMax] = useState('10')
  const [loading, setLoading] = useState(false)
  const [proxyServicesLocal, setProxyServicesLocal] = useState([])
  const toast = useToast()

  useEffect(() => {
    // Parent may already load 代理服务; only fetch if not provided.
    if (Array.isArray(proxyServicesProp) && proxyServicesProp.length) return
    let cancelled = false
    api.get('/proxy-services')
      .then(d => { if (!cancelled) setProxyServicesLocal(d?.services || []) })
      .catch(() => { if (!cancelled) setProxyServicesLocal([]) })
    return () => { cancelled = true }
  }, [proxyServicesProp])

  const proxyServices = (Array.isArray(proxyServicesProp) && proxyServicesProp.length)
    ? proxyServicesProp
    : proxyServicesLocal

  const grantedIds = useMemo(
    () => new Set((grantedNodes || []).map(n => Number(n.id))),
    [grantedNodes],
  )
  const grantedSvcIds = useMemo(
    () => new Set((grantedProxyServiceIds || []).map(Number).filter(id => id > 0)),
    [grantedProxyServiceIds],
  )
  const available = useMemo(
    () => (allNodes || []).filter(n => !grantedIds.has(Number(n.id))),
    [allNodes, grantedIds],
  )
  const proxyIds = useMemo(() => {
    if (proxyNodeIds instanceof Set) return proxyNodeIds
    return new Set((proxyNodeIds || []).map(Number))
  }, [proxyNodeIds])

  // 代理 tab = 每条代理服务一项；授权粒度是 service_id（同节点其它协议不会一起授权）。
  const proxyOpts = useMemo(() => {
    const nodeById = Object.fromEntries((allNodes || []).map(n => [Number(n.id), n]))
    const opts = []
    for (const s of proxyServices || []) {
      const nids = (s.deployed_node_ids || []).map(Number).filter(id => id > 0 && nodeById[id])
      if (!nids.length) continue
      const already = grantedSvcIds.has(Number(s.id))
      const proto = PROTO_LABEL[s.protocol] || s.protocol || ''
      const key = `svc:${s.id}`
      let label = s.name || '(未命名服务)'
      const showNid = nids[0]
      const node = nodeById[showNid]
      if (nids.length === 1) {
        if (node?.name && node.name !== s.name) label = `${label} · ${node.name}`
      } else {
        label = `${label} · ${nids.length} 节点`
      }
      if (proto) label = `[${proto}] ${label}`
      if (already) label = `${label} · 已授权`
      opts.push({ value: key, label, disabled: already })
    }
    if (opts.length) return opts
    // Fallback: no service list — only ungranted proxy-hosting nodes (node-level).
    return available
      .filter(n => proxyIds.has(Number(n.id)))
      .map(n => ({ value: String(n.id), label: n.name }))
  }, [proxyServices, allNodes, grantedSvcIds, available, proxyIds])

  if (!allNodes?.length) {
    return <Empty desc={<Link to="/nodes" className="text-emerald-600 text-xs font-semibold">请先创建节点</Link>} />
  }
  if (!available.length && !proxyOpts.some(o => !o.disabled)) {
    return <div className="text-xs text-ink-mut">所有线路均已授权</div>
  }

  const submit = async (e) => {
    e.preventDefault()
    if (!selected.length) { toast('请选择节点或代理协议', 'error'); return }
    const nodeIds = []
    const serviceIds = []
    for (const v of selected) {
      const s = String(v)
      if (s.startsWith('svc:')) {
        const sid = Number(s.slice(4))
        if (sid > 0 && !grantedSvcIds.has(sid)) serviceIds.push(sid)
      } else {
        const n = Number(s)
        if (n > 0 && !grantedIds.has(n)) nodeIds.push(n)
      }
    }
    if (!nodeIds.length && !serviceIds.length) {
      toast('所选项目均已授权', 'error')
      return
    }
    setLoading(true)
    try {
      const body = { max_forwards: Number(max) }
      if (nodeIds.length) body.node_ids = nodeIds
      if (serviceIds.length) body.proxy_service_ids = serviceIds
      await api.post(`/users/${userId}/grants`, body)
      const parts = []
      if (serviceIds.length) parts.push(`${serviceIds.length} 个代理协议`)
      if (nodeIds.length) parts.push(`${nodeIds.length} 个节点`)
      toast(`已授权 ${parts.join(' · ')}`); setSelected([]); onDone()
    } catch (err) { toast(err.message, 'error') } finally { setLoading(false) }
  }
  return (
    <>
      <div className="text-xs font-bold text-ink-mut uppercase tracking-wider mb-3">授权线路</div>
      <form onSubmit={submit} className="space-y-3 max-w-xl">
        <div className="grid grid-cols-[140px_1fr] gap-4 items-center">
          <label className="fl">节点规则数上限</label>
          <input className="input-field font-mono" type="number" min="1" value={max} onChange={e => setMax(e.target.value)} style={{ maxWidth: 160 }} />
          <label className="fl">节点 <span className="text-ink-mut font-normal text-xs">(可多选)</span></label>
          <Select value={selected} onChange={setSelected} placeholder="-- 选择 --" searchable multiple tabs
            groups={[
              { label: '单点', options: available.filter(n => n.node_type !== 'composite').map(n => ({ value: n.id, label: n.name })) },
              { label: '组合', options: available.filter(n => n.node_type === 'composite').map(n => ({ value: n.id, label: n.name })) },
              { label: '代理', options: proxyOpts.filter(o => !o.disabled) },
            ]} />
        </div>
        <button type="submit" disabled={loading} className="btn-primary text-xs">授权</button>
      </form>
    </>
  )
}

function GrantedNodesCard({ userId, nodes, grants, allNodes, allUsers, userSpeedLimitMBytes, proxyNodeIds = [], proxyServiceIds = [], onDone, embedded = false }) {
  const [tab, setTab] = useState('single')
  const [selected, setSelected] = useState(new Set())
  const [revoking, setRevoking] = useState(false)
  const [showPaste, setShowPaste] = useState(false)
  const [proxyServices, setProxyServices] = useState([])
  const toast = useToast()
  const confirm = useConfirm()

  useEffect(() => {
    let cancelled = false
    api.get('/proxy-services')
      .then(d => { if (!cancelled) setProxyServices(d?.services || []) })
      .catch(() => { if (!cancelled) setProxyServices([]) })
    return () => { cancelled = true }
  }, [])

  const proxyIds = useMemo(() => {
    const ids = new Set(proxyNodeIds instanceof Set ? [...proxyNodeIds].map(Number) : (proxyNodeIds || []).map(Number))
    // Prefer live 代理服务 coverage so chip/list match the 代理服务 page.
    for (const s of proxyServices || []) {
      for (const nid of s.deployed_node_ids || []) {
        if (nid > 0) ids.add(Number(nid))
      }
    }
    return ids
  }, [proxyNodeIds, proxyServices])

  const grantedSvcIds = useMemo(
    () => new Set((proxyServiceIds || []).map(Number).filter(id => id > 0)),
    [proxyServiceIds],
  )

  const grantByNode = {}
  nodes.forEach((n, i) => { grantByNode[n.id] = grants[i] })

  // 代理 tab 行：按已授权的 proxy_service 展开。节点名来自全部节点，
  // 不要求该 VPS 也在 user_nodes（否则授权代理会误显示成单点）。
  const proxyRows = useMemo(() => {
    const allById = Object.fromEntries((allNodes || []).map(n => [Number(n.id), n]))
    const rows = []
    for (const s of proxyServices || []) {
      if (!grantedSvcIds.has(Number(s.id))) continue
      const nids = (s.deployed_node_ids || []).map(Number).filter(id => id > 0)
      if (!nids.length) {
        rows.push({
          key: `svc:${s.id}:0`,
          serviceId: Number(s.id),
          nodeId: 0,
          node: null,
          service: s,
        })
        continue
      }
      for (const nid of nids) {
        rows.push({
          key: `svc:${s.id}:${nid}`,
          serviceId: Number(s.id),
          nodeId: nid,
          node: allById[nid] || null,
          service: s,
        })
      }
    }
    return rows
  }, [proxyServices, grantedSvcIds, allNodes])

  const singleNodes = nodes.filter(n => n.node_type !== 'composite')
  const compositeNodes = nodes.filter(n => n.node_type === 'composite')
  // 单点/组合只展示显式授权的 user_nodes；授权代理不再写入节点授权。
  const tabNodes = tab === 'composite' ? compositeNodes : tab === 'proxy' ? [] : singleNodes

  const toggleOne = (id) => setSelected(s => {
    const next = new Set(s)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })
  const toggleAll = () => {
    if (tab === 'proxy') {
      const allKeys = proxyRows.map(r => r.key)
      const allSelected = allKeys.length > 0 && allKeys.every(k => selected.has(k))
      if (allSelected) setSelected(s => { const next = new Set(s); allKeys.forEach(k => next.delete(k)); return next })
      else setSelected(s => { const next = new Set(s); allKeys.forEach(k => next.add(k)); return next })
      return
    }
    const allIds = tabNodes.map(n => n.id)
    const allSelected = allIds.every(id => selected.has(id))
    if (allSelected) setSelected(s => { const next = new Set(s); allIds.forEach(id => next.delete(id)); return next })
    else setSelected(s => { const next = new Set(s); allIds.forEach(id => next.add(id)); return next })
  }

  const batchRevoke = async () => {
    if (!selected.size) return
    if (tab === 'proxy') {
      const svcIds = [...new Set([...selected].map(k => {
        const m = String(k).match(/^svc:(\d+)/)
        return m ? Number(m[1]) : 0
      }).filter(id => id > 0))]
      if (!svcIds.length) return
      if (!(await confirm({ title: '批量撤销', message: `确认撤销 ${svcIds.length} 个代理协议的授权？`, confirmText: '撤销', danger: true }))) return
      setRevoking(true)
      try {
        await api.post(`/users/${userId}/grants/batch-revoke`, { proxy_service_ids: svcIds })
        toast(`已撤销 ${svcIds.length} 个代理协议`)
        setSelected(new Set())
        onDone()
      } catch (err) { toast(err.message, 'error') } finally { setRevoking(false) }
      return
    }
    const ids = [...selected]
    if (!ids.length) return
    if (!(await confirm({ title: '批量撤销', message: `确认撤销 ${ids.length} 个节点的授权？`, confirmText: '撤销', danger: true }))) return
    setRevoking(true)
    try {
      await api.post(`/users/${userId}/grants/batch-revoke`, { node_ids: ids })
      toast(`已撤销 ${ids.length} 个节点`)
      setSelected(new Set())
      onDone()
    } catch (err) { toast(err.message, 'error') } finally { setRevoking(false) }
  }

  const revokeOne = async (nodeId) => {
    try { await api.del(`/users/${userId}/grants/${nodeId}`); toast('已撤销'); onDone() } catch (err) { toast(err.message, 'error') }
  }

  const revokeProxyService = async (serviceId) => {
    try {
      await api.del(`/users/${userId}/proxy-grants/${serviceId}`)
      toast('已撤销代理协议')
      onDone()
    } catch (err) { toast(err.message, 'error') }
  }

  const resetNodeTraffic = async (nodeId) => {
    if (!(await confirm({ title: '重置节点流量', message: '清零该用户在此节点上的已用流量？', confirmText: '清零', danger: true }))) return
    try { await api.post(`/users/${userId}/nodes/${nodeId}/reset-traffic`); toast('已重置'); onDone() } catch (err) { toast(err.message, 'error') }
  }

  const copyGrants = () => {
    const lines = nodes.map(n => {
      const g = grantByNode[n.id]
      const parts = [n.name]
      parts.push(`max=${g?.max_forwards ?? 10}`)
      const gb = g?.traffic_quota_bytes ? Number((g.traffic_quota_bytes / 1073741824).toFixed(2)) : 0
      parts.push(`quota=${gb}GB`)
      parts.push(`rate=${g?.rate_limit_mbytes || 0}`)
      return parts.join(' | ')
    })
    const text = lines.join('\n')
    copyToClipboard(text).then(() => toast(`已复制 ${nodes.length} 个节点授权`)).catch(() => toast('复制失败', 'error'))
  }

  const shellClass = embedded
    ? 'detail-panel'
    : 'card mb-5 soft-panel'

  return (
    <div className={shellClass}>
      <div className={embedded ? 'detail-panel-header' : 'card-header'}>
        <div className="min-w-0">
          <h3 className={embedded ? 'detail-panel-title' : 'text-sm font-bold'}>已授权线路</h3>
          {embedded && <div className="detail-panel-sub">{nodes.length} 条单点/组合 · {proxyRows.length} 个代理协议</div>}
        </div>
        <div className="flex items-center gap-1.5 ml-auto">
          {nodes.length > 0 && <button onClick={copyGrants} className="btn-secondary text-xs">复制授权</button>}
          <button onClick={() => setShowPaste(true)} className="btn-secondary text-xs">粘贴授权</button>
        </div>
      </div>
      {(nodes.length > 0 || proxyRows.length > 0) && (
        <div className="flex items-center gap-1.5 px-[22px] py-2.5 border-b border-line-soft">
          {[['single', '单点', singleNodes.length], ['composite', '组合', compositeNodes.length], ['proxy', '代理', proxyRows.length]].map(([key, label, n]) => (
            <button key={key} onClick={() => { setTab(key); setSelected(new Set()) }}
              className={`chip-btn ${tab === key ? 'is-active' : ''}`}>{label} {n}</button>
          ))}
          {selected.size > 0 && (
            <button onClick={batchRevoke} disabled={revoking} className="btn-danger-sm text-xs ml-auto">
              撤销选中 ({selected.size})
            </button>
          )}
        </div>
      )}
      {tab === 'proxy' ? (
        proxyRows.length > 0 ? (
          <TableBox>
          <table className="tbl">
            <thead><tr>
              <th className="w-8"><input type="checkbox" className="accent-emerald-600"
                checked={proxyRows.length > 0 && proxyRows.every(r => selected.has(r.key))}
                onChange={toggleAll} /></th>
              <th>代理协议</th><th>节点</th><th className="text-right">操作</th>
            </tr></thead>
            <tbody>
              {proxyRows.map(row => {
                const n = row.node
                const proto = PROTO_LABEL[row.service?.protocol] || row.service?.protocol || ''
                const svcLabel = proto ? `[${proto}] ${row.service?.name || ''}` : (row.service?.name || `服务 #${row.serviceId}`)
                return (
                  <tr key={row.key}>
                    <td><input type="checkbox" className="accent-emerald-600" checked={selected.has(row.key)} onChange={() => toggleOne(row.key)} /></td>
                    <td className="font-semibold">{svcLabel}</td>
                    <td className="font-semibold">
                      {n ? <Link to={`/nodes/${n.id}`} className="text-emerald-600 hover:underline">{n.name}</Link> : <span className="text-ink-mut">—</span>}
                    </td>
                    <td className="text-right">
                      <button onClick={() => revokeProxyService(row.serviceId)} className="btn-danger-sm text-xs">撤销协议</button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
          </TableBox>
        ) : (
          <Empty title="暂无已授权的代理协议" />
        )
      ) : tabNodes.length > 0 ? (
        <TableBox>
        <table className="tbl">
          <thead><tr>
            <th className="w-8"><input type="checkbox" className="accent-emerald-600"
              checked={tabNodes.length > 0 && tabNodes.every(n => selected.has(n.id))}
              onChange={toggleAll} /></th>
            <th>节点</th><th>类型</th><th>规则上限</th><th>流量配额</th><th>限速</th><th>已用</th><th className="w-16"></th><th className="text-right">操作</th>
          </tr></thead>
          <tbody>
            {tabNodes.map(n => (
              <tr key={n.id}>
                <td><input type="checkbox" className="accent-emerald-600" checked={selected.has(n.id)} onChange={() => toggleOne(n.id)} /></td>
                <td className="font-semibold">
                  <Link to={`/nodes/${n.id}`} className="text-emerald-600 hover:underline">{n.name}</Link>
                </td>
                <td><NodeTypeBadge type={n.node_type} /></td>
                <td>
                  <PerNodeMaxForwardsForm userId={userId} nodeId={n.id} maxForwards={grantByNode[n.id]?.max_forwards} onDone={onDone} />
                </td>
                <td>
                  <PerNodeQuotaForm userId={userId} nodeId={n.id} quotaBytes={grantByNode[n.id]?.traffic_quota_bytes} onDone={onDone} />
                </td>
                <td>
                  <PerNodeRateForm userId={userId} nodeId={n.id} rateMBytes={grantByNode[n.id]?.rate_limit_mbytes} onDone={onDone} />
                  {!grantByNode[n.id]?.rate_limit_mbytes && userSpeedLimitMBytes > 0 && (
                    <div className="mt-1 text-[11px] text-ink-mut">取用户全局值 {userSpeedLimitMBytes} Mbps</div>
                  )}
                </td>
                <td className="font-mono text-sm">
                  {fmtTrafficGB(grantByNode[n.id]?.traffic_used_bytes, grantByNode[n.id]?.traffic_quota_bytes)}
                </td>
                <td>
                  {grantByNode[n.id]?.traffic_quota_bytes > 0 && grantByNode[n.id]?.traffic_used_bytes > 0 && (
                    <button onClick={() => resetNodeTraffic(n.id)} className="btn-danger-sm text-xs">重置</button>
                  )}
                </td>
                <td className="text-right">
                  <button onClick={() => revokeOne(n.id)} className="btn-danger-sm text-xs">撤销</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        </TableBox>
      ) : nodes.length > 0 || proxyRows.length > 0 ? (
        <Empty title={tab === 'composite' ? '暂无已授权的组合线路' : '暂无已授权的单点线路'} />
      ) : (
        <Empty title="尚未授权任何线路" />
      )}
      <div className="p-5 border-t border-line-soft">
        <GrantNodeForm
          userId={userId}
          allNodes={allNodes}
          grantedNodes={nodes}
          proxyNodeIds={proxyIds}
          proxyServices={proxyServices}
          grantedProxyServiceIds={[...grantedSvcIds]}
          onDone={onDone}
        />
      </div>
      {showPaste && <PasteGrantsModal open={showPaste} onClose={() => setShowPaste(false)} onDone={onDone}
        allNodes={allNodes} allUsers={allUsers} preSelectedUserIds={[Number(userId)]} />}
    </div>
  )
}

export default GrantedNodesCard
