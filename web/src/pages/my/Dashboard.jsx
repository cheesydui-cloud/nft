import { useState, useEffect } from 'react'
import { api } from '../../lib/api'
import { pct, fmtTrafficGB, fmtDate, isExpired, nullStr } from '../../lib/fmt'
import { useIsMobile } from '../../lib/useIsMobile'
import { Layout } from '../../components/Layout'
import { Loading, Empty, Badge, NodeTypeBadge } from '../../components/ui'
import { TableBox } from '../../components/page'

export default function MyDashboard() {
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [tab, setTab] = useState('single')
  const isMobile = useIsMobile()

  // 授权节点的展示顺序是个人偏好，只存本浏览器不上服务器；键按用户 id 区分，
  // 同一浏览器切换账号互不串扰。不在名单里的节点（新授权）按服务器顺序垫底。
  const [nodeOrder, setNodeOrder] = useState([])
  const [dragIdx, setDragIdx] = useState(null)
  useEffect(() => {
    if (!data?.user?.id) return
    try { setNodeOrder(JSON.parse(localStorage.getItem(`my.nodeOrder.${data.user.id}`) || '[]')) } catch { setNodeOrder([]) }
  }, [data?.user?.id])

  useEffect(() => {
    api.get('/my').then(setData).catch(console.error).finally(() => setLoading(false))
  }, [])

  if (loading) return <Layout><Loading /></Layout>
  if (!data) return <Layout><Empty title="无法加载数据" /></Layout>
  const { user, nodes = [], grants = [], rules = [], show_rate } = data

  const expiresAt = user.expires_at && user.expires_at > 0 ? user.expires_at : null

  const grantByNode = {}
  nodes.forEach((n, i) => { grantByNode[n.id] = grants[i] })
  // 排序在 grantByNode 建好之后：nodes 与 grants 按下标对齐，排序副本不动原数组。
  const orderIdx = new Map(nodeOrder.map((id, i) => [id, i]))
  const orderedNodes = [...nodes].sort((a, b) => {
    const ia = orderIdx.get(a.id) ?? Infinity
    const ib = orderIdx.get(b.id) ?? Infinity
    return ia === ib ? 0 : ia - ib
  })
  const singleNodes = orderedNodes.filter(n => n.node_type !== 'composite')
  const compositeNodes = orderedNodes.filter(n => n.node_type === 'composite')
  const tabNodes = tab === 'composite' ? compositeNodes : singleNodes
  const saveNodeOrder = (ids) => {
    setNodeOrder(ids)
    localStorage.setItem(`my.nodeOrder.${user.id}`, JSON.stringify(ids))
  }
  const onDropRow = (toIdx) => {
    if (dragIdx === null || dragIdx === toIdx) { setDragIdx(null); return }
    const list = [...tabNodes]
    const [moved] = list.splice(dragIdx, 1)
    list.splice(toIdx, 0, moved)
    setDragIdx(null)
    const other = tab === 'composite' ? singleNodes : compositeNodes
    const ids = tab === 'composite'
      ? [...other.map(n => n.id), ...list.map(n => n.id)]
      : [...list.map(n => n.id), ...other.map(n => n.id)]
    saveNodeOrder(ids)
  }

  return (
    <Layout>
      {user.disabled && (
        <div className="mb-4 px-4 py-3 bg-red-50 border border-red-200 rounded-lg text-red-600 text-sm font-medium">
          您的账号已被禁用：{nullStr(user.disable_reason)}。请联系管理员。
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-[1.15fr_1fr] gap-[18px] mb-[22px]">
        {/* Quota */}
        <div className="card flex flex-col">
          <div className="px-6 py-[22px] flex-1 flex flex-col">
            <h3 className="text-[16px] font-bold mb-5">我的配额</h3>
            <div className="flex items-center gap-4 py-3 border-b border-line-soft">
              <div className="w-[120px] flex-shrink-0 text-[14px] text-ink-soft">用户名</div>
              <div className="text-[14.5px]"><span className="font-semibold">{user.username}</span></div>
            </div>
            <div className="flex items-center gap-4 py-3 border-b border-line-soft">
              <div className="w-[120px] flex-shrink-0 text-[14px] text-ink-soft">规则配额</div>
              <div className="text-[14.5px]"><span className="font-mono">{rules.length}</span> <span className="text-ink-mut">/</span> <span className="font-mono">{user.max_forwards}</span></div>
            </div>
            {show_rate && (
              <div className="flex items-center gap-4 py-3 border-b border-line-soft">
                <div className="w-[120px] flex-shrink-0 text-[14px] text-ink-soft">倍率</div>
                <div className="text-[14.5px] font-mono">×{user.billing_rate ?? 1}</div>
              </div>
            )}
            <div className="flex items-center gap-4 py-3 border-b border-line-soft">
              <div className="w-[120px] flex-shrink-0 text-[14px] text-ink-soft">{show_rate ? '流量（计费）' : '流量'}</div>
              <div className="text-[14.5px] font-mono">
                {(() => {
                  const rate = user.billing_rate ?? 1
                  const displayUsed = Math.round(user.traffic_used_bytes * rate)
                  const displayQuota = user.traffic_quota_bytes
                  return (
                    <>
                      {fmtTrafficGB(displayUsed, displayQuota)}
                      {user.traffic_quota_bytes > 0 && <span className="text-green-600 dark:text-green-400"> ({pct(displayUsed, displayQuota)}%)</span>}
                    </>
                  )
                })()}
              </div>
            </div>
            <div className="flex items-center gap-4 py-3 border-b border-line-soft">
              <div className="w-[120px] flex-shrink-0 text-[14px] text-ink-soft">{show_rate ? '累计流量（计费）' : '累计流量'}</div>
              <div className="text-[14.5px] font-mono">
                {fmtTrafficGB(Math.round((user.total_traffic_used_bytes || 0) * (user.billing_rate ?? 1)), 0)}
              </div>
            </div>
            <div className="flex items-center gap-4 py-3">
              <div className="w-[120px] flex-shrink-0 text-[14px] text-ink-soft">到期时间</div>
              <div className="text-[14.5px]">
                {expiresAt ? <>{fmtDate(expiresAt)} {isExpired(expiresAt) && <Badge color="red">已过期</Badge>}</> : '永不过期'}
              </div>
            </div>
          </div>
        </div>

        {/* Announcement area */}
        <AnnouncementArea />
      </div>

      {/* Granted nodes */}
      <div className="card">
        <div className="card-header">
          <h3 className="text-[15px] font-bold">已授权节点</h3>
        </div>
        {nodes.length > 0 && (
          <div className="flex items-center gap-1.5 px-[22px] py-2.5 border-b border-line-soft">
            {[['single', '单点', singleNodes.length], ['composite', '组合', compositeNodes.length]].map(([key, label, n]) => (
              <button key={key} onClick={() => setTab(key)}
                className={`px-3.5 py-1 rounded-full text-xs border transition-colors ${
                  tab === key ? 'bg-emerald-500 text-white border-emerald-500' : 'bg-surface text-ink-soft border-line hover:border-ink-mut'
                }`}>{label} {n}</button>
            ))}
          </div>
        )}
        {tabNodes.length > 0 ? (<>
          {/* Desktop table */}
          {!isMobile && <TableBox>
          <table className="tbl">
            <thead><tr><th>节点</th><th>类型</th>{show_rate && <th>倍率</th>}<th>状态</th><th>限速</th><th>本节点上限</th></tr></thead>
            <tbody>
              {tabNodes.map((n, i) => {
                const g = grantByNode[n.id]
                return (
                  <tr key={n.id}
                    onDragOver={e => e.preventDefault()}
                    onDrop={() => onDropRow(i)}
                    className={dragIdx === i ? 'opacity-50' : ''}>
                    <td className="font-semibold">
                      <span className="text-ink-mut mr-1.5 select-none cursor-move flex-none" title="拖拽排序（仅保存在本浏览器）"
                        draggable onDragStart={() => setDragIdx(i)}>
                        <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="currentColor"><circle cx="9" cy="6" r="1.5"/><circle cx="15" cy="6" r="1.5"/><circle cx="9" cy="12" r="1.5"/><circle cx="15" cy="12" r="1.5"/><circle cx="9" cy="18" r="1.5"/><circle cx="15" cy="18" r="1.5"/></svg>
                      </span>
                      {n.name}
                      {(n.roles & 2) !== 0 && <Badge color="blue" className="ml-1.5">中间层</Badge>}
                    </td>
                    <td><NodeTypeBadge type={n.node_type} /></td>
                    {show_rate && <td><Badge color="blue">×{n.rate_multiplier ?? 1}</Badge>{n.unidirectional && <Badge color="amber" className="ml-1">单向</Badge>}</td>}
                    <td><NodeOnline node={n} /></td>
                    <td className="font-mono text-xs">{g?.rate_limit_mbytes > 0 ? `${g.rate_limit_mbytes} Mbps` : '不限'}</td>
                    <td className="font-mono">{g?.max_forwards ?? '--'}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
          </TableBox>}
          {/* Mobile cards */}
          {isMobile && <div>
            {tabNodes.map(n => {
              const g = grantByNode[n.id]
              return (
                <div key={n.id} className="mobile-card">
                  <div className="flex items-center justify-between mb-1">
                    <span className="font-semibold">
                      {n.name}
                      {(n.roles & 2) !== 0 && <Badge color="blue" className="ml-1.5">中间层</Badge>}
                    </span>
                    <NodeOnline node={n} />
                  </div>
                  <div className="flex items-center gap-2 text-xs text-ink-soft flex-wrap">
                    <NodeTypeBadge type={n.node_type} />
                    {show_rate && <Badge color="blue">×{n.rate_multiplier ?? 1}</Badge>}
                    {n.unidirectional && <Badge color="amber">单向</Badge>}
                    {g?.rate_limit_mbytes > 0 && <>
                      <span className="text-ink-mut">·</span>
                      <span className="font-mono">{g.rate_limit_mbytes} Mbps</span>
                    </>}
                  </div>
                </div>
              )
            })}
          </div>}
        </>) : nodes.length > 0 ? (
          <Empty title={tab === 'composite' ? '暂无已授权的组合节点' : '暂无已授权的单点节点'} />
        ) : <Empty title="管理员尚未为您授权任何节点" desc="请联系管理员。" />}
      </div>
    </Layout>
  )
}

// Online/offline (or disabled) status for a granted node. The server resolves
// composite nodes' online state from their children before sending.
function NodeOnline({ node }) {
  if (node.disabled) return <Badge color="amber">禁用</Badge>
  return node.online === 1 ? <Badge color="green">在线</Badge> : <Badge color="gray">离线</Badge>
}

// AnnouncementArea shows admin-published notices on the user dashboard.
function AnnouncementArea() {
  const [items, setItems] = useState([])
  const [loading, setLoading] = useState(true)

  // Read / write read IDs from localStorage
  const readKey = 'announcements.read'
  const [readIds, setReadIds] = useState(() => {
    try { return new Set(JSON.parse(localStorage.getItem(readKey) || '[]')) } catch { return new Set() }
  })
  const markRead = (id) => {
    setReadIds(prev => {
      if (prev.has(id)) return prev
      const next = new Set(prev)
      next.add(id)
      localStorage.setItem(readKey, JSON.stringify([...next]))
      return next
    })
  }
  const markAllRead = () => {
    const allIds = new Set(items.map(a => a.id))
    setReadIds(allIds)
    localStorage.setItem(readKey, JSON.stringify([...allIds]))
  }

  useEffect(() => {
    api.get('/my/announcements').then(d => setItems(d?.announcements || [])).catch(console.error).finally(() => setLoading(false))
  }, [])

  const unreadCount = items.filter(a => !readIds.has(a.id)).length

  return (
    <div className="card flex flex-col">
      <div className="px-6 py-[22px] flex-1 flex flex-col">
        <div className="flex items-center justify-between mb-5">
          <h3 className="text-[16px] font-bold">公告</h3>
          {unreadCount > 0 && (
            <button onClick={markAllRead} className="text-xs text-emerald-600 font-semibold hover:underline">全部已读</button>
          )}
        </div>
        {loading ? (
          <div className="text-sm text-ink-mut py-8 text-center">加载中…</div>
        ) : items.length === 0 ? (
          <div className="text-sm text-ink-mut py-8 text-center">暂无公告</div>
        ) : (
          <div className="flex flex-col gap-4 overflow-y-auto flex-1 min-h-0">
            {items.map(a => {
              const isRead = readIds.has(a.id)
              return (
                <div key={a.id} className={`border-b border-line-soft pb-3 last:border-0 last:pb-0 cursor-pointer transition-opacity ${isRead ? 'opacity-60' : ''}`}
                  onClick={() => markRead(a.id)}>
                  <div className="flex items-center gap-2 mb-1">
                    <span className="font-semibold text-[14px]">{a.title}</span>
                    {!isRead && <Badge color="red">新</Badge>}
                    {a.target_user_id > 0 && <Badge color="blue">私信</Badge>}
                  </div>
                  <div className="text-[13px] text-ink-soft whitespace-pre-wrap">{a.content}</div>
                  <div className="text-[11px] text-ink-mut mt-1">{fmtDate(a.created_at)}</div>
                </div>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
