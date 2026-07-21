import { useState } from 'react'
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

function GrantNodeForm({ userId, allNodes, grantedNodes, onDone }) {
  const [nodeIds, setNodeIds] = useState([])
  const [max, setMax] = useState('10')
  const [loading, setLoading] = useState(false)
  const toast = useToast()
  if (!allNodes?.length) return <Empty desc={<Link to="/nodes" className="text-emerald-600 text-xs font-semibold">请先创建节点</Link>} />

  const grantedIds = new Set((grantedNodes || []).map(n => n.id))
  const available = allNodes.filter(n => !grantedIds.has(n.id))
  if (!available.length) return <div className="text-xs text-ink-mut">所有节点均已授权</div>

  const submit = async (e) => {
    e.preventDefault()
    if (!nodeIds.length) { toast('请选择节点', 'error'); return }
    setLoading(true)
    try {
      await api.post(`/users/${userId}/grants`, { node_ids: nodeIds.map(Number), max_forwards: Number(max) })
      toast(`已授权 ${nodeIds.length} 个节点`); setNodeIds([]); onDone()
    } catch (err) { toast(err.message, 'error') } finally { setLoading(false) }
  }
  return (
    <>
      <div className="text-xs font-bold text-ink-mut uppercase tracking-wider mb-3">授权新节点</div>
      <form onSubmit={submit} className="space-y-3 max-w-xl">
        <div className="grid grid-cols-[140px_1fr] gap-4 items-center">
          <label className="fl">节点规则数上限</label>
          <input className="input-field font-mono" type="number" min="1" value={max} onChange={e => setMax(e.target.value)} style={{ maxWidth: 160 }} />
          <label className="fl">节点 <span className="text-ink-mut font-normal text-xs">(可多选)</span></label>
          <Select value={nodeIds} onChange={setNodeIds} placeholder="-- 选择 --" searchable multiple tabs
            groups={[
              { label: '单点', options: available.filter(n => n.node_type !== 'composite').map(n => ({ value: n.id, label: n.name })) },
              { label: '组合', options: available.filter(n => n.node_type === 'composite').map(n => ({ value: n.id, label: n.name })) },
            ]} />
        </div>
        <button type="submit" disabled={loading} className="btn-primary text-xs">授权</button>
      </form>
    </>
  )
}

function GrantedNodesCard({ userId, nodes, grants, allNodes, allUsers, userSpeedLimitMBytes, onDone, embedded = false }) {
  const [tab, setTab] = useState('single')
  const [selected, setSelected] = useState(new Set())
  const [revoking, setRevoking] = useState(false)
  const [showPaste, setShowPaste] = useState(false)
  const toast = useToast()
  const confirm = useConfirm()

  const singleNodes = nodes.filter(n => n.node_type !== 'composite')
  const compositeNodes = nodes.filter(n => n.node_type === 'composite')
  const tabNodes = tab === 'composite' ? compositeNodes : singleNodes
  const grantByNode = {}
  nodes.forEach((n, i) => { grantByNode[n.id] = grants[i] })

  const toggleOne = (id) => setSelected(s => {
    const next = new Set(s)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })
  const toggleAll = () => {
    const allIds = tabNodes.map(n => n.id)
    const allSelected = allIds.every(id => selected.has(id))
    if (allSelected) setSelected(s => { const next = new Set(s); allIds.forEach(id => next.delete(id)); return next })
    else setSelected(s => { const next = new Set(s); allIds.forEach(id => next.add(id)); return next })
  }

  const batchRevoke = async () => {
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
          <h3 className={embedded ? 'detail-panel-title' : 'text-sm font-bold'}>已授权节点</h3>
          {embedded && <div className="detail-panel-sub">{nodes.length} 个节点 · 单点/组合配额与限速</div>}
        </div>
        <div className="flex items-center gap-1.5 ml-auto">
          {nodes.length > 0 && <button onClick={copyGrants} className="btn-secondary text-xs">复制授权</button>}
          <button onClick={() => setShowPaste(true)} className="btn-secondary text-xs">粘贴授权</button>
        </div>
      </div>
      {nodes.length > 0 && (
        <div className="flex items-center gap-1.5 px-[22px] py-2.5 border-b border-line-soft">
          {[['single', '单点', singleNodes.length], ['composite', '组合', compositeNodes.length]].map(([key, label, n]) => (
            <button key={key} onClick={() => { setTab(key); setSelected(new Set()) }}
              className={`px-3 py-1 rounded-md text-xs font-semibold border transition-colors ${
                tab === key
                  ? 'bg-emerald-600 text-white border-emerald-600'
                  : 'bg-surface text-ink-soft border-line hover:border-ink-mut'
              }`}>{label} {n}</button>
          ))}
          {selected.size > 0 && (
            <button onClick={batchRevoke} disabled={revoking} className="btn-danger-sm text-xs ml-auto">
              撤销选中 ({selected.size})
            </button>
          )}
        </div>
      )}
      {tabNodes.length > 0 ? (
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
      ) : nodes.length > 0 ? (
        <Empty title={tab === 'composite' ? '暂无已授权的组合节点' : '暂无已授权的单点节点'} />
      ) : (
        <Empty title="尚未授权任何节点" />
      )}
      <div className="p-5 border-t border-line-soft">
        <GrantNodeForm userId={userId} allNodes={allNodes} grantedNodes={nodes} onDone={onDone} />
      </div>
      {showPaste && <PasteGrantsModal open={showPaste} onClose={() => setShowPaste(false)} onDone={onDone}
        allNodes={allNodes} allUsers={allUsers} preSelectedUserIds={[Number(userId)]} />}
    </div>
  )
}

export default GrantedNodesCard
