import { useState, useEffect, useMemo } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { fmtBytes, fmtTime, nullStr, nullInt } from '../lib/fmt'
import { Layout, useBlur, useUser } from '../components/Layout'
import { Loading, Empty, Badge, ErrorState, SensText, NodeTypeBadge, NodeBillingBadges } from '../components/ui'
import { ProxyURIEditor } from '../components/ProxyURIEditor'
import { PageHeader, TableBox } from '../components/page'
import { useIsMobile } from '../lib/useIsMobile'

const DAY = 86400

export default function Dashboard() {
  const [data, setData] = useState(null)
  const [users, setUsers] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const blurred = useBlur()
  const isMobile = useIsMobile()
  const { user } = useUser()

  const load = () => {
    setLoading(true)
    setError('')
    Promise.all([
      api.get('/dashboard'),
      api.get('/users').catch(() => null),
    ]).then(([dash, userList]) => {
      setData(dash)
      setUsers(userList?.users || null)
    }).catch(e => {
      setError(e.message || '加载失败')
      setData(null)
    }).finally(() => setLoading(false))
  }

  useEffect(() => { load() }, [])

  const attention = useMemo(() => buildAttention(data, users), [data, users])
  const opsSummary = useMemo(() => buildOpsSummary(data, users), [data, users])

  if (loading) return <Layout><Loading /></Layout>
  if (error || !data) {
    return (
      <Layout>
        <ErrorState title="无法加载概览" desc={error || '请稍后重试。'} onRetry={load} />
      </Layout>
    )
  }

  const { nodes = [], node_traffic = {}, rule_count = 0, rule_count_by_node = {}, total_bytes = 0, user_count = 0, today_raw_bytes = 0 } = data
  const onlineCount = nodes.filter(n => !n.disabled && n.online === 1).length
  const offline = nodes.filter(n => n.disabled || n.online !== 1).map(n => n.name)
  const ruleCount = rule_count_by_node

  return (
    <Layout>
      <PageHeader
        title="运营概览"
        badge={
          <div className="inline-flex items-center gap-2 px-3.5 py-[6px] rounded-full text-[13px] font-semibold text-green-700 dark:text-green-400 bg-green-500/10 border border-green-500/[.28]">
            <span className="w-[7px] h-[7px] rounded-full bg-green-500 shadow-[0_0_0_3px_rgba(34,197,94,0.2)]" />
            {onlineCount} 节点在线
          </div>
        }
      />

      <div className="grid grid-cols-2 lg:grid-cols-5 gap-4 mb-6">
        <StatCard label="活跃转发" value={rule_count} sub="正在转发的规则"
          icon={<path d="M5 12h14M13 6l6 6-6 6"/>} />
        <StatCard label="在线节点" value={onlineCount} unit={` /${nodes.length}`}
          sub={offline.length ? `${offline.slice(0, 2).join('、')}${offline.length > 2 ? ` 等 ${offline.length} 个` : ''} 离线` : '全部在线'} accent
          icon={<><rect x="3" y="4" width="18" height="6" rx="1.5"/><rect x="3" y="14" width="18" height="6" rx="1.5"/></>} />
        <StatCard label="总流量（计费）" value={fmtBytes(total_bytes)} sub="累计计费当量"
          icon={<><path d="M3 3v18h18"/><path d="M7 14l4-4 3 3 5-6"/></>} />
        <StatCard label="当日流量（实际）" value={fmtBytes(today_raw_bytes || 0)} sub="今日实际上行+下行 · 北京时间0点切日"
          icon={<><path d="M12 3v18"/><path d="m5 12 7-7 7 7"/></>} />
        <StatCard label="用户" value={user_count} sub="系统用户数"
          icon={<><path d="M16 21v-2a4 4 0 0 0-4-4H7a4 4 0 0 0-4 4v2"/><circle cx="9.5" cy="7" r="3.5"/></>} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-[1fr_1fr] gap-[18px] mb-6">
        <div className="card">
          <div className="card-header justify-between">
            <h3 className="text-[15px] font-bold">需关注</h3>
            <span className="text-[12.5px] text-ink-mut">{attention.length ? `${attention.length} 项` : '状态正常'}</span>
          </div>
          {attention.length === 0 ? (
            <OpsSummaryPanel summary={opsSummary} />
          ) : (
            <div className="attention-list">
              {attention.map(item => (
                <Link key={item.key} to={item.to} className="attention-item">
                  <span className={`attention-dot ${item.tone}`} />
                  <div className="min-w-0 flex-1">
                    <div className="text-[13.5px] font-semibold text-ink truncate">{item.title}</div>
                    <div className="text-[12.5px] text-ink-mut mt-0.5 truncate">{item.desc}</div>
                  </div>
                  <Badge color={item.tone === 'danger' ? 'red' : item.tone === 'warn' ? 'amber' : 'blue'}>{item.tag}</Badge>
                </Link>
              ))}
            </div>
          )}
        </div>

        <div className="card">
          <div className="card-header justify-between">
            <h3 className="text-[15px] font-bold">节点状态</h3>
            <span className="text-[12.5px] text-ink-mut">{nodes.length} 个节点</span>
          </div>
          {nodes.length ? (<>
            {!isMobile && <TableBox>
            <table className="tbl">
              <thead><tr><th>节点名</th><th>地址</th><th>类型</th><th>规则</th><th>流量</th><th>状态</th><th>心跳</th></tr></thead>
              <tbody>
                {nodes.map(n => (
                  <tr key={n.id}>
                    <td>
                      <Link to={`/nodes/${n.id}`} className="font-semibold text-emerald-600 hover:underline inline-flex items-center gap-1.5 flex-wrap">
                        {n.name}
                        <NodeBillingBadges node={n} />
                      </Link>
                    </td>
                    <td className="font-mono text-xs text-ink-soft"><SensText blurred={blurred}>{n.relay_host || n.address || '--'}</SensText></td>
                    <td><NodeTypeBadge type={n.node_type} /></td>
                    <td className="font-mono text-ink-soft">{ruleCount[n.id] || 0}</td>
                    <td className="font-mono text-xs text-ink-mut">{fmtBytes(node_traffic[n.id] || 0)}</td>
                    <td><NodeStatus node={n} /></td>
                    <td className="font-mono text-ink-mut text-xs">{fmtTime(n.last_seen)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            </TableBox>}
            {isMobile && <div>
              {nodes.map(n => (
                <Link key={n.id} to={`/nodes/${n.id}`} className="mobile-card block no-underline text-ink">
                  <div className="flex items-center justify-between mb-1.5">
                    <span className="font-semibold text-emerald-600 inline-flex items-center gap-1.5 flex-wrap">
                      {n.name}
                      <NodeBillingBadges node={n} />
                    </span>
                    <NodeStatus node={n} />
                  </div>
                  <div className="flex items-center gap-2 text-xs text-ink-soft flex-wrap">
                    <NodeTypeBadge type={n.node_type} />
                    <span className="text-ink-mut">·</span>
                    <span className="font-mono">{ruleCount[n.id] || 0} 条规则</span>
                    <span className="text-ink-mut">·</span>
                    <span className="font-mono text-ink-mut">{fmtBytes(node_traffic[n.id] || 0)}</span>
                  </div>
                </Link>
              ))}
            </div>}
          </>) : <Empty title="暂无节点" />}
        </div>
      </div>

      <div className="hidden md:block">
        <ProxyURIEditor username={user?.username} blurred={blurred} />
      </div>
    </Layout>
  )
}

function buildAttention(data, users) {
  if (!data) return []
  const items = []
  const now = Math.floor(Date.now() / 1000)
  const nodes = data.nodes || []

  for (const n of nodes) {
    if (n.disabled) {
      items.push({
        key: `node-dis-${n.id}`,
        to: `/nodes/${n.id}`,
        tone: 'warn',
        tag: '禁用',
        title: n.name,
        desc: '节点已禁用',
      })
      continue
    }
    const lastErr = nullStr(n.last_error)
    if (n.node_type !== 'composite' && lastErr) {
      items.push({
        key: `node-err-${n.id}`,
        to: `/nodes/${n.id}`,
        tone: 'danger',
        tag: '错误',
        title: n.name,
        desc: lastErr.slice(0, 80),
      })
      continue
    }
    if (n.online !== 1) {
      items.push({
        key: `node-off-${n.id}`,
        to: `/nodes/${n.id}`,
        tone: 'danger',
        tag: '离线',
        title: n.name,
        desc: n.last_seen ? `最后心跳 ${fmtTime(n.last_seen)}` : '暂无心跳',
      })
    }
  }

  if (Array.isArray(users)) {
    for (const u of users) {
      if (u.role === 'admin') continue
      const exp = nullInt(u.expires_at) || 0
      if (exp > 0) {
        if (exp <= now) {
          items.push({
            key: `user-exp-${u.id}`,
            to: `/users/${u.id}`,
            tone: 'danger',
            tag: '已到期',
            title: u.username,
            desc: '账号已过期，请续期或禁用',
          })
        } else if (exp - now <= 7 * DAY) {
          const days = Math.max(1, Math.ceil((exp - now) / DAY))
          items.push({
            key: `user-soon-${u.id}`,
            to: `/users/${u.id}`,
            tone: 'warn',
            tag: `${days} 天内到期`,
            title: u.username,
            desc: '账号即将到期',
          })
        }
      }
      const quota = u.traffic_quota_bytes || 0
      const used = u.traffic_used_bytes || 0
      if (quota > 0) {
        const ratio = used / quota
        if (ratio >= 1) {
          items.push({
            key: `user-quota-${u.id}`,
            to: `/users/${u.id}`,
            tone: 'danger',
            tag: '流量用尽',
            title: u.username,
            desc: `${fmtBytes(used)} / ${fmtBytes(quota)}`,
          })
        } else if (ratio >= 0.9) {
          items.push({
            key: `user-quota-high-${u.id}`,
            to: `/users/${u.id}`,
            tone: 'warn',
            tag: '流量将尽',
            title: u.username,
            desc: `已用 ${Math.round(ratio * 100)}%`,
          })
        }
      }
    }
  }

  const rank = { danger: 0, warn: 1, info: 2 }
  items.sort((a, b) => (rank[a.tone] ?? 9) - (rank[b.tone] ?? 9))
  return items.slice(0, 5)
}

// When attention is empty, fill the card with dense ops summaries so the
// overview still carries signal (top traffic, recent offline, expiring soon).
function buildOpsSummary(data, users) {
  const nodes = data?.nodes || []
  const traffic = data?.node_traffic || {}
  const now = Math.floor(Date.now() / 1000)

  const topNodes = [...nodes]
    .map(n => ({ id: n.id, name: n.name, bytes: traffic[n.id] || 0 }))
    .filter(n => n.bytes > 0)
    .sort((a, b) => b.bytes - a.bytes)
    .slice(0, 3)

  const topUsers = (Array.isArray(users) ? users : [])
    .filter(u => u.role !== 'admin')
    .map(u => {
      const rate = u.billing_rate > 0 ? u.billing_rate : 1
      const real = u.traffic_used_bytes || 0
      return {
        id: u.id,
        username: u.username,
        real,
        display: Math.round(real * rate),
        rate,
      }
    })
    .filter(u => u.real > 0)
    .sort((a, b) => b.real - a.real)
    .slice(0, 3)

  const recentOffline = nodes
    .filter(n => !n.disabled && n.online !== 1 && n.node_type !== 'composite')
    .map(n => ({
      id: n.id,
      name: n.name,
      last_seen: n.last_seen || 0,
    }))
    .sort((a, b) => (b.last_seen || 0) - (a.last_seen || 0))
    .slice(0, 3)

  const expiringSoon = (Array.isArray(users) ? users : [])
    .filter(u => u.role !== 'admin' && !u.disabled)
    .map(u => {
      const exp = nullInt(u.expires_at) || 0
      return { id: u.id, username: u.username, exp }
    })
    .filter(u => u.exp > now && u.exp - now <= 14 * DAY)
    .sort((a, b) => a.exp - b.exp)
    .slice(0, 3)

  return { topNodes, topUsers, recentOffline, expiringSoon }
}

function OpsSummaryPanel({ summary }) {
  const { topNodes = [], topUsers = [], recentOffline = [], expiringSoon = [] } = summary || {}
  const empty = !topNodes.length && !topUsers.length && !recentOffline.length && !expiringSoon.length
  if (empty) {
    return (
      <div className="px-5 py-10 text-center text-[13px] text-ink-mut">
        暂无需要处理的事项 · 运营数据也还不多
      </div>
    )
  }

  const Section = ({ title, children, to, more }) => (
    <div className="px-4 py-3 border-b border-line-soft last:border-b-0">
      <div className="flex items-center justify-between mb-2">
        <span className="text-[12px] font-semibold text-ink-soft uppercase tracking-wide">{title}</span>
        {to && more ? <Link to={to} className="text-[11.5px] text-emerald-600 hover:underline">{more}</Link> : null}
      </div>
      <div className="space-y-1.5">{children}</div>
    </div>
  )

  const Row = ({ to, left, right }) => (
    <Link to={to} className="flex items-center justify-between gap-2 text-[13px] hover:bg-raised/60 rounded-md px-1.5 py-1 -mx-1.5 transition-colors">
      <span className="min-w-0 truncate font-medium text-ink">{left}</span>
      <span className="flex-none font-mono text-[12px] text-ink-mut">{right}</span>
    </Link>
  )

  return (
    <div className="divide-y divide-line-soft">
      {topNodes.length > 0 && (
        <Section title="流量 Top 节点" to="/nodes" more="节点">
          {topNodes.map(n => (
            <Row key={n.id} to={`/nodes/${n.id}`} left={n.name} right={fmtBytes(n.bytes)} />
          ))}
        </Section>
      )}
      {topUsers.length > 0 && (
        <Section title="用量 Top 用户" to="/users" more="用户">
          {topUsers.map(u => (
            <Row
              key={u.id}
              to={`/users/${u.id}`}
              left={u.username}
              right={`${fmtBytes(u.real)}${u.rate !== 1 ? ` · 视 ${fmtBytes(u.display)}` : ''}`}
            />
          ))}
        </Section>
      )}
      {recentOffline.length > 0 && (
        <Section title="最近离线" to="/nodes" more="节点">
          {recentOffline.map(n => (
            <Row
              key={n.id}
              to={`/nodes/${n.id}`}
              left={n.name}
              right={n.last_seen ? fmtTime(n.last_seen) : '暂无心跳'}
            />
          ))}
        </Section>
      )}
      {expiringSoon.length > 0 && (
        <Section title="14 天内到期" to="/users" more="用户">
          {expiringSoon.map(u => {
            const days = Math.max(1, Math.ceil((u.exp - Math.floor(Date.now() / 1000)) / DAY))
            return (
              <Row key={u.id} to={`/users/${u.id}`} left={u.username} right={`${days} 天后`} />
            )
          })}
        </Section>
      )}
      <div className="px-4 py-2.5 text-[11.5px] text-ink-mut">暂无紧急事项 · 以上为运营摘要</div>
    </div>
  )
}

function StatCard({ label, value, unit, sub, accent, icon }) {
  return (
    <div className="card stat-card">
      <div className="flex items-center justify-between">
        <span className="text-[13px] text-ink-soft font-medium">{label}</span>
        {icon && <svg className="w-[18px] h-[18px] text-ink-mut opacity-50" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">{icon}</svg>}
      </div>
      <div className="mt-3.5 flex items-baseline gap-0.5">
        <span className={`text-[28px] sm:text-[34px] font-bold leading-[1.05] tracking-tight ${accent ? 'text-green-600 dark:text-green-400' : 'text-ink'}`}>{value}</span>
        {unit && <span className="text-[16px] font-semibold text-ink-mut">{unit}</span>}
      </div>
      <div className="stat-sub text-[12.5px] text-ink-mut truncate">{sub || ' '}</div>
    </div>
  )
}

function NodeStatus({ node }) {
  if (node.disabled) return <Badge color="amber">禁用</Badge>
  if (node.node_type === 'composite') {
    return node.online === 1 ? <Badge color="green">在线</Badge> : <Badge color="gray">离线</Badge>
  }
  const lastErr = nullStr(node.last_error)
  if (lastErr) return <Badge color="red" title={lastErr}>错误</Badge>
  return node.online === 1 ? <Badge color="green">在线</Badge> : <Badge color="gray">离线</Badge>
}
