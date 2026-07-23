import { useState, useEffect, useMemo } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { api } from '../../lib/api'
import { fmtBytes, fmtTrafficGB, pct, fmtDate, expiryBadge, nullStr } from '../../lib/fmt'
import { Layout, useToast, useBlur, useCopyFmt } from '../../components/Layout'
import { Loading, Empty, Badge, Modal, useConfirm, ProbeChainButton } from '../../components/ui'
import { IdentityBar, DetailTabs, StatTile, SectionCard, TableBox, InfoGrid } from '../../components/page'
import { copyToClipboard } from '../../lib/clipboard'
import { useRuleSpeed, fmtSpeed } from '../../lib/useSpeed'
import { uriToClashYaml } from '../../lib/yaml-convert'
import { fetchNodeRoles, nodeHasRole, ROLE_LANDING } from '../../lib/landing'
import { createLimiter } from '../../lib/limiter'
import { RuleFormModal, ruleToForm, ruleFormToPayload } from '../../components/RuleFormModal'
import UserConfigCard from './UserConfigCard'
import GrantedNodesCard from './GrantedNodesCard'
import LandingSourceCard from './LandingSourceCard'

// Cap concurrent probe-chain calls for 「测试全部」 (each request fans out hops).
const detailProbeLimit = createLimiter(6)

export default function UserDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const toast = useToast()
  const blurred = useBlur()
  const { copyFmt } = useCopyFmt()
  const ruleSpeeds = useRuleSpeed()
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [allUsers, setAllUsers] = useState([])
  const [tab, setTab] = useState('overview')
  const [newPassword, setNewPassword] = useState(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [editRule, setEditRule] = useState(null)
  const [bindings, setBindings] = useState([])
  const [nodeRoles, setNodeRoles] = useState({})
  const [probeAllTrigger, setProbeAllTrigger] = useState(0)
  const confirm = useConfirm()

  const load = () => {
    setLoading(true)
    api.get(`/users/${id}`).then(setData).catch(console.error).finally(() => setLoading(false))
  }
  useEffect(load, [id])
  useEffect(() => { api.get('/users').then(d => setAllUsers(d?.users || [])) }, [])
  useEffect(() => { api.get('/node-bindings').then(d => setBindings(d?.bindings || [])).catch(console.error) }, [])
  useEffect(() => { fetchNodeRoles().then(setNodeRoles).catch(() => setNodeRoles({})) }, [])
  useEffect(() => { setTab('overview'); setCreateOpen(false); setEditRule(null); setProbeAllTrigger(0) }, [id])

  // Acting as this user: only their granted relay nodes and landing-marked exits.
  const rawLanding = data?.landing_nodes || []
  const ruleFormLandingNodes = useMemo(
    () => rawLanding.filter(n => nodeHasRole(nodeRoles, n, ROLE_LANDING)),
    [rawLanding, nodeRoles],
  )

  if (loading) return <Layout><Loading /></Layout>
  if (!data) return <Layout><Empty title="用户不存在" /></Layout>

  const { user, nodes = [], grants = [], all_nodes = [], rules = [], landing_nodes = [] } = data
  const nodeMap = Object.fromEntries(all_nodes.map(n => [n.id, n]))
  const isRegularUser = user.role === 'user'
  const expiresAt = user.expires_at && user.expires_at > 0 ? user.expires_at : null
  const rate = user.billing_rate ?? 1
  const realUsed = user.traffic_used_bytes || 0
  const billableUsed = realUsed * rate
  const quota = user.traffic_quota_bytes || 0
  const billablePct = quota > 0 ? Number(pct(billableUsed, quota)) : 0
  const realPct = quota > 0 ? Number(pct(realUsed, quota)) : 0
  // Remaining is computed on the user-visible (billable) side — that's what
  // the quota gate enforces when billing_rate ≠ 1.
  const remainingBillable = quota > 0 ? Math.max(0, quota - billableUsed) : null
  const remainingReal = quota > 0 && rate > 0 ? Math.max(0, (quota / rate) - realUsed) : null
  const exp = expiresAt ? expiryBadge(expiresAt) : null
  // Live total of this user's rules (admin-only overview signal).
  const liveUp = rules.reduce((s, r) => s + ((ruleSpeeds[r.id] || {}).up || 0), 0)
  const liveDown = rules.reduce((s, r) => s + ((ruleSpeeds[r.id] || {}).down || 0), 0)
  const liveActive = rules.filter(r => {
    const sp = ruleSpeeds[r.id] || {}
    return (sp.up || 0) + (sp.down || 0) > 0
  }).length

  const toggleUser = async () => {
    try { await api.post(`/users/${id}/toggle`); toast(user.disabled ? '已启用' : '已禁用'); load() } catch (err) { toast(err.message, 'error') }
  }
  const resetTraffic = async () => {
    try { await api.post(`/users/${id}/reset-traffic`); toast('已重置'); load() } catch (err) { toast(err.message, 'error') }
  }
  const deleteUser = async () => {
    try { await api.del(`/users/${id}`); toast('已删除'); navigate('/users') } catch (err) { toast(err.message, 'error') }
  }
  const resetPassword = async () => {
    try {
      const d = await api.post(`/users/${id}/reset-password`)
      if (d?.new_password) setNewPassword(d.new_password)
      return d
    } catch (err) { toast(err.message, 'error') }
  }

  const statusBadge = user.disabled
    ? <Badge color="amber">已禁用</Badge>
    : <Badge color="green">正常</Badge>

  // Asia/Shanghai calendar day raw traffic (0:00–23:59); billable = raw × rate.
  const todayRaw = data?.today_raw_bytes || 0
  const todayBillable = todayRaw * rate
  const todayDay = data?.today_day || ''
  const yesterdayRaw = data?.yesterday_raw_bytes || 0
  const yesterdayBillable = yesterdayRaw * rate
  const yesterdayDay = data?.yesterday_day || ''

  const chips = isRegularUser ? [
    {
      label: '流量',
      value: fmtTrafficGB(billableUsed, quota),
      tone: quota > 0 && billablePct >= 100 ? 'tone-danger' : quota > 0 && billablePct >= 80 ? 'tone-warn' : 'tone-blue',
      title: '用户视角累计（已用×计费倍率）',
    },
    {
      label: '规则',
      value: `${rules.length}${user.max_forwards > 0 ? ` / ${user.max_forwards}` : ''}`,
      tone: 'tone-violet',
    },
    {
      label: '节点',
      value: `${nodes.length}`,
      tone: 'tone-teal',
    },
    {
      label: '落地',
      value: `${landing_nodes.length}`,
      tone: 'tone-blue',
    },
    {
      label: '到期',
      value: expiresAt ? fmtDate(expiresAt) : '永不过期',
      tone: exp?.color === 'red' ? 'tone-danger' : exp?.color === 'gray' ? 'tone-warn' : 'tone-ok',
    },
    user.speed_limit_mbytes > 0 ? {
      label: '限速',
      value: `${user.speed_limit_mbytes} Mbps`,
      tone: 'tone-violet',
    } : null,
  ].filter(Boolean) : []

  const tabs = [{ id: 'overview', label: '概览' }]
  if (isRegularUser) {
    tabs.push(
      { id: 'config', label: '配置' },
      { id: 'grants', label: '授权线路', count: nodes.length },
      { id: 'landing', label: '落地节点', count: landing_nodes.length },
    )
  }
  tabs.push({ id: 'rules', label: '规则', count: rules.length })

  // If role changes mid-session, fall back to overview rather than rendering empty.
  const activeTab = tabs.some(t => t.id === tab) ? tab : 'overview'
  const avatar = (user.username || '?').slice(0, 2).toUpperCase()

  const overviewItems = isRegularUser
    ? [
        { label: '用户名', value: user.username, accent: true },
        { label: '角色', value: <span className="font-mono">{user.role}</span> },
        { label: '分组', value: user.group_name ? <Badge color="blue">{user.group_name}</Badge> : <span className="text-ink-mut">未分组</span> },
        { label: '规则配额', value: <span className="font-mono">{rules.length} / {user.max_forwards || '∞'}</span> },
        { label: '真实流量', value: (
            <span className="font-mono">
              {fmtBytes(realUsed)}
              {quota > 0 && <span className="text-ink-mut text-xs ml-1">相对配额 {realPct}%</span>}
              {user.traffic_reset_days > 0 && <span className="text-ink-mut text-xs ml-1">每{user.traffic_reset_days}天重置</span>}
            </span>
          )},
        { label: '用户视角流量', value: (
            <span className="font-mono">
              {fmtTrafficGB(billableUsed, quota)}
              {quota > 0 && ` (${billablePct}%)`}
              <span className="text-ink-mut text-xs ml-1">真实×{rate}</span>
            </span>
          )},
        { label: '剩余额度', value: (
            <span className="font-mono">
              {quota > 0
                ? <>{fmtBytes(remainingBillable)} <span className="text-ink-mut text-xs">视角</span>
                    {rate !== 1 && remainingReal != null && (
                      <span className="text-ink-mut text-xs ml-1">· 真实约 {fmtBytes(remainingReal)}</span>
                    )}
                  </>
                : '不限额'}
            </span>
          )},
        { label: '计费倍率', value: <span className="font-mono">×{rate}</span> },
        { label: '实时网速', value: (
            <span className="font-mono text-xs">
              <span className="text-emerald-600">↑{fmtSpeed(liveUp)}</span>
              {' '}
              <span className="text-emerald-600">↓{fmtSpeed(liveDown)}</span>
              <span className="text-ink-mut ml-1">{liveActive}/{rules.length} 活跃</span>
            </span>
          )},
        { label: '全局限速', value: <span className="font-mono">{user.speed_limit_mbytes > 0 ? `${user.speed_limit_mbytes} Mbps` : '不限'}</span> },
        { label: '到期时间', value: (
            <span className="font-mono">
              {expiresAt ? <>{fmtDate(expiresAt)}{exp ? <Badge color={exp.color} className="ml-1">{exp.label}</Badge> : null}</> : '永不过期'}
            </span>
          )},
        { label: '状态', value: (
            <span>
              {user.disabled ? (
                <><Badge color="amber">已禁用</Badge> <span className="text-ink-soft text-xs ml-1">原因：{nullStr(user.disable_reason)}</span></>
              ) : <Badge color="green">正常</Badge>}
            </span>
          )},
        ...(user.admin_note ? [{ label: '管理备注', value: <span className="text-ink-soft text-[13px]">{user.admin_note}</span> }] : []),
      ]
    : [
        { label: '用户名', value: user.username, accent: true },
        { label: '角色', value: <span className="font-mono">{user.role}</span> },
      ]

  const trafficTone = !quota ? 'blue'
    : billablePct >= 100 ? 'danger'
    : billablePct >= 80 ? 'warn'
    : 'ok'
  const remainTone = !quota ? 'blue'
    : billablePct >= 100 ? 'danger'
    : billablePct >= 80 ? 'warn'
    : 'ok'

  return (
    <Layout>
      <IdentityBar
        backTo="/users"
        backLabel="返回用户列表"
        avatar={avatar}
        title={user.username}
        badge={statusBadge}
        meta={`ID ${user.id} · ${user.role}`}
        chips={chips}
        actions={isRegularUser ? (
          <>
            <button onClick={toggleUser} className="btn-secondary text-xs">{user.disabled ? '启用' : '禁用'}</button>
            <button onClick={resetTraffic} className="btn-secondary text-xs">重置流量</button>
            <button onClick={resetPassword} className="btn-secondary text-xs">重置密码</button>
            <button onClick={deleteUser} className="btn-secondary text-xs text-red-600 border-red-200 hover:border-red-400">删除</button>
          </>
        ) : null}
      />

      {isRegularUser && (
        <div className="mb-4 grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div className="rounded-2xl border border-line-soft bg-surface/80 px-4 py-3">
            <div className="flex items-baseline justify-between gap-2 mb-2">
              <span className="text-[12px] font-bold text-ink tracking-wide">今日</span>
              <span className="text-[11px] text-ink-mut tabular-nums">{todayDay || '北京时间'} · 0:00–23:59</span>
            </div>
            <div className="flex items-end gap-5">
              <div>
                <div className="text-[11px] text-ink-mut mb-0.5">真实</div>
                <div className="text-[18px] font-bold tabular-nums text-ink leading-tight">{fmtBytes(todayRaw)}</div>
              </div>
              <div>
                <div className="text-[11px] text-ink-mut mb-0.5">视角{rate !== 1 ? ` ×${rate}` : ''}</div>
                <div className="text-[18px] font-bold tabular-nums text-emerald-700 dark:text-emerald-400 leading-tight">{fmtBytes(todayBillable)}</div>
              </div>
            </div>
          </div>
          <div className="rounded-2xl border border-line-soft bg-surface/80 px-4 py-3">
            <div className="flex items-baseline justify-between gap-2 mb-2">
              <span className="text-[12px] font-bold text-ink tracking-wide">昨日</span>
              <span className="text-[11px] text-ink-mut tabular-nums">{yesterdayDay || '上一自然日'}</span>
            </div>
            <div className="flex items-end gap-5">
              <div>
                <div className="text-[11px] text-ink-mut mb-0.5">真实</div>
                <div className="text-[18px] font-bold tabular-nums text-ink leading-tight">{fmtBytes(yesterdayRaw)}</div>
              </div>
              <div>
                <div className="text-[11px] text-ink-mut mb-0.5">视角{rate !== 1 ? ` ×${rate}` : ''}</div>
                <div className="text-[18px] font-bold tabular-nums text-emerald-700 dark:text-emerald-400 leading-tight">{fmtBytes(yesterdayBillable)}</div>
              </div>
            </div>
          </div>
        </div>
      )}

      <DetailTabs tabs={tabs} active={activeTab} onChange={setTab} />

      {activeTab === 'overview' && (
        <div className="space-y-4">
          {isRegularUser && (
            <>
              <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
                <StatTile
                  label="真实流量"
                  value={fmtBytes(realUsed)}
                  hint={quota > 0 ? `相对配额 ${realPct}% · 不乘倍率` : '出站+入站累计 · 不乘倍率'}
                  tone="blue"
                />
                <StatTile
                  label="用户视角流量"
                  value={fmtTrafficGB(billableUsed, quota)}
                  hint={quota > 0 ? `已用 ${billablePct}% · 真实×${rate}` : `倍率 ×${rate} · 不限额`}
                  tone={trafficTone}
                />
                <StatTile
                  label="剩余额度"
                  value={quota > 0 ? fmtBytes(remainingBillable) : '∞'}
                  hint={quota > 0
                    ? (rate !== 1 && remainingReal != null
                      ? `视角剩余 · 真实约 ${fmtBytes(remainingReal)}`
                      : '按用户视角计算')
                    : '未设置配额'}
                  tone={remainTone}
                />
                <StatTile
                  label="实时网速"
                  value={<span className="text-[18px] sm:text-[20px]"><span className="text-emerald-600">↑{fmtSpeed(liveUp)}</span> <span className="text-emerald-600">↓{fmtSpeed(liveDown)}</span></span>}
                  hint={`${liveActive} / ${rules.length} 条规则有流量`}
                  tone="teal"
                />
              </div>
              <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
                <StatTile
                  label="规则使用"
                  value={`${rules.length}${user.max_forwards > 0 ? ` / ${user.max_forwards}` : ''}`}
                  hint={user.max_forwards > 0 ? '已用 / 配额' : '规则总数 · 不限配额'}
                  tone="violet"
                />
                <StatTile
                  label="授权线路"
                  value={String(nodes.length)}
                  hint="已授权转发线路"
                  tone="teal"
                />
                <StatTile
                  label="计费倍率"
                  value={`×${rate}`}
                  hint="用户视角 = 真实 × 倍率"
                  tone="blue"
                />
                <StatTile
                  label="到期时间"
                  value={expiresAt ? fmtDate(expiresAt) : '永不过期'}
                  hint={exp ? exp.label : '未设置到期'}
                  tone={exp?.color === 'red' ? 'danger' : exp?.color === 'gray' ? 'warn' : 'ok'}
                />
              </div>
            </>
          )}

          <div className="detail-panel">
            <div className="detail-panel-header">
              <h3 className="detail-panel-title">基本信息</h3>
              {isRegularUser && (
                <div className="flex items-center gap-2">
                  <button type="button" className="btn-secondary text-xs" onClick={() => setTab('config')}>编辑配置</button>
                  <button type="button" className="btn-secondary text-xs" onClick={() => setTab('grants')}>管理授权</button>
                </div>
              )}
            </div>
            <div className="detail-panel-body">
              <InfoGrid items={overviewItems} />
            </div>
          </div>

          {isRegularUser && rules.length > 0 && (
            <SectionCard title="规则实时网速" subtitle={`共 ${rules.length} 条 · 仅管理员可见`}>
              <TableBox>
                <table className="tbl">
                  <thead>
                    <tr>
                      <th>名称</th>
                      <th>节点</th>
                      <th>实时</th>
                      <th className="text-right">真实用量</th>
                      <th className="text-right">显示用量</th>
                    </tr>
                  </thead>
                  <tbody>
                    {[...rules]
                      .sort((a, b) => {
                        const sa = ruleSpeeds[a.id] || {}
                        const sb = ruleSpeeds[b.id] || {}
                        return ((sb.up || 0) + (sb.down || 0)) - ((sa.up || 0) + (sa.down || 0))
                      })
                      .slice(0, 8)
                      .map(r => {
                        const sp = ruleSpeeds[r.id] || { up: 0, down: 0 }
                        return (
                          <tr key={r.id}>
                            <td className="font-semibold">
                              <Link to={`/rules/${r.id}`} className="text-emerald-600 hover:underline">{r.name}</Link>
                            </td>
                            <td className="font-mono text-xs text-ink-soft">{nodeMap[r.node_id]?.name || `#${r.node_id}`}</td>
                            <td className="font-mono text-xs whitespace-nowrap">
                              <span className="text-emerald-600">↑{fmtSpeed(sp.up)}</span>
                              {' '}
                              <span className="text-emerald-600">↓{fmtSpeed(sp.down)}</span>
                            </td>
                            <td className="text-right font-mono text-xs text-ink-mut">{fmtBytes(r.exit_bytes || 0)}</td>
                            <td className="text-right font-mono text-xs">{fmtBytes(Math.round((r.exit_bytes || 0) * rate))}</td>
                          </tr>
                        )
                      })}
                  </tbody>
                </table>
              </TableBox>
              {rules.length > 8 && (
                <div className="mt-2 text-right">
                  <button type="button" className="text-xs text-emerald-600 font-semibold hover:underline" onClick={() => setTab('rules')}>
                    查看全部 {rules.length} 条规则
                  </button>
                </div>
              )}
            </SectionCard>
          )}
        </div>
      )}

      {activeTab === 'config' && isRegularUser && (
        <div className="detail-panel">
          <div className="detail-panel-header">
            <div>
              <h3 className="detail-panel-title">可编辑配置</h3>
              <div className="detail-panel-sub">到期、配额、限速与备注</div>
            </div>
          </div>
          <div className="detail-panel-body">
            <UserConfigCard
              userId={id}
              expiresAt={expiresAt}
              maxForwards={user.max_forwards}
              quotaBytes={user.traffic_quota_bytes}
              resetDays={user.traffic_reset_days}
              adminNote={user.admin_note || ''}
              billingRate={user.billing_rate}
              speedLimitMBytes={user.speed_limit_mbytes || 0}
              onDone={load}
            />
          </div>
        </div>
      )}

      {activeTab === 'grants' && isRegularUser && (
        <GrantedNodesCard
          userId={id}
          nodes={nodes}
          grants={grants}
          allNodes={all_nodes}
          allUsers={allUsers}
          userSpeedLimitMBytes={user.speed_limit_mbytes || 0}
          onDone={load}
          embedded
        />
      )}

      {activeTab === 'landing' && isRegularUser && (
        <LandingSourceCard
          userId={id}
          subURL={user.landing_sub_url}
          uris={user.landing_uris}
          nodes={landing_nodes}
          blurred={blurred}
          embedded
        />
      )}

      {activeTab === 'rules' && (
        <SectionCard
          title="该用户的规则"
          subtitle={`${rules.length} 条`}
          actions={
            <div className="flex items-center gap-2">
              {rules.length > 0 && (
                <button
                  type="button"
                  className="btn-secondary text-xs"
                  onClick={() => setProbeAllTrigger(t => t + 1)}
                >
                  测试全部
                </button>
              )}
              <button type="button" className="btn-primary text-xs" onClick={() => setCreateOpen(true)}>
                ＋ 替用户创建规则
              </button>
            </div>
          }
        >
          {rules.length ? (
            <TableBox>
              <table className="tbl">
                <thead>
                  <tr>
                    <th>ID</th><th>名称</th><th>节点</th><th>网速</th>
                    <th className="text-right">真实用量</th>
                    <th className="text-right">显示用量(×{rate})</th>
                    <th className="text-right">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {rules.map(r => {
                    // Per-rule only — never fall back to node totals. Multiple rules
                    // on the same relay/composite share a node_id; node speed would
                    // make every row show the same rate when one of them is busy.
                    const sp = ruleSpeeds[r.id] || { up: 0, down: 0 }
                    const copyRuleLink = async () => {
                      const parts = []
                      if (r.relay_uri) parts.push(copyFmt === 'yaml' ? uriToClashYaml(r.relay_uri) : r.relay_uri)
                      if (r.relay_uri_v6) parts.push(copyFmt === 'yaml' ? uriToClashYaml(r.relay_uri_v6) : r.relay_uri_v6)
                      if (!parts.length) {
                        if (r.entry) parts.push(r.entry)
                        if (r.entry_v6) parts.push(r.entry_v6)
                      }
                      const text = parts.filter(Boolean).join('\n').trim()
                      if (!text) { toast('该规则没有可复制的链接', 'error'); return }
                      try {
                        await copyToClipboard(text)
                        toast('规则链接已复制')
                      } catch {
                        toast('复制失败', 'error')
                      }
                    }
                    const deleteRule = async () => {
                      if (!(await confirm({ title: '删除规则', message: `确认删除规则「${r.name}」？`, confirmText: '删除', danger: true }))) return
                      try {
                        await api.del(`/rules/${r.id}`)
                        toast('已删除')
                        load()
                      } catch (err) {
                        toast(err.message, 'error')
                      }
                    }
                    return (
                      <tr key={r.id}>
                        <td className="font-mono text-xs text-ink-mut">{r.id}</td>
                        <td className="font-semibold">
                          <Link to={`/rules/${r.id}`} className="text-emerald-600 hover:underline">{r.name}</Link>
                        </td>
                        <td className="font-mono text-ink-soft">{nodeMap[r.node_id]?.name || `#${r.node_id}`}</td>
                        <td className="font-mono text-xs whitespace-nowrap">
                          <span className="inline-flex items-center gap-1.5">
                            <span className="text-emerald-600">↑{fmtSpeed(sp.up)}</span>
                            <span className="text-emerald-600">↓{fmtSpeed(sp.down)}</span>
                          </span>
                        </td>
                        <td className="text-right font-mono text-xs text-ink-mut">{fmtBytes(r.exit_bytes || 0)}</td>
                        <td className="text-right font-mono text-xs">{fmtBytes(Math.round((r.exit_bytes || 0) * rate))}</td>
                        <td className="text-right">
                          <div className="inline-flex items-center gap-2.5 flex-wrap justify-end">
                            <ProbeChainButton
                              ruleId={r.id}
                              probeAllTrigger={probeAllTrigger}
                              limit={detailProbeLimit}
                            />
                            <button onClick={copyRuleLink} className="text-emerald-600 text-xs font-semibold hover:underline">复制</button>
                            <button onClick={() => setEditRule(r)} className="text-emerald-600 text-xs font-semibold hover:underline">编辑</button>
                            <button onClick={deleteRule} className="text-red-600 text-xs font-semibold hover:underline">删除</button>
                          </div>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </TableBox>
          ) : <Empty title="该用户尚无规则" desc="可点击右上角「替用户创建规则」。" />}
        </SectionCard>
      )}

      <RuleFormModal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title={`替 ${user.username} 创建规则`}
        submitLabel="创建规则"
        nodes={nodes}
        // Only this user's landing-marked exits (same scope as the user panel).
        landingNodes={ruleFormLandingNodes.length ? ruleFormLandingNodes : undefined}
        bindings={bindings}
        initial={{ owner_id: Number(id) }}
        onSubmit={async (form) => {
          const payload = ruleFormToPayload({ ...form, owner_id: Number(id) })
          await api.post('/rules', payload)
          toast('规则已创建')
          setCreateOpen(false)
          load()
        }}
      />

      <RuleFormModal
        open={!!editRule}
        onClose={() => setEditRule(null)}
        title="编辑规则"
        submitLabel="保存并重下发"
        nodes={nodes}
        landingNodes={ruleFormLandingNodes.length ? ruleFormLandingNodes : undefined}
        bindings={bindings}
        initial={editRule ? { ...ruleToForm(editRule), owner_id: Number(id) } : null}
        onSubmit={async (form) => {
          const payload = ruleFormToPayload({ ...form, owner_id: Number(id) })
          await api.put(`/rules/${editRule.id}`, payload)
          toast('已保存并重下发')
          setEditRule(null)
          load()
        }}
      />

      <Modal open={!!newPassword} onClose={() => setNewPassword(null)} title="新密码">
        <p className="text-sm text-ink-soft mb-3">新密码只显示这一次，请复制并妥善保存。关闭后将无法再次查看。</p>
        <div className="flex items-center gap-2">
          <code className="flex-1 font-mono text-sm bg-raised border border-line rounded-lg px-3 py-2.5 break-all select-all">{newPassword}</code>
          <button onClick={() => copyToClipboard(newPassword).then(() => toast('已复制')).catch(() => toast('复制失败', 'error'))} className="btn-primary flex-none px-4">复制</button>
        </div>
        <div className="flex justify-end mt-5">
          <button onClick={() => setNewPassword(null)} className="btn-secondary">关闭</button>
        </div>
      </Modal>
    </Layout>
  )
}
