import { useState, useEffect } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { api } from '../../lib/api'
import { fmtBytes } from '../../lib/fmt'
import { Layout, useToast, useBlur } from '../../components/Layout'
import { Loading, Empty } from '../../components/ui'
import { DetailHeader, SectionCard, TableBox } from '../../components/page'
import UserInfoCard from './UserInfoCard'
import UserConfigCard from './UserConfigCard'
import GrantedNodesCard from './GrantedNodesCard'
import LandingSourceCard from './LandingSourceCard'

export default function UserDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const toast = useToast()
  const blurred = useBlur()
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)

  const [allUsers, setAllUsers] = useState([])

  const load = () => {
    setLoading(true)
    api.get(`/users/${id}`).then(setData).catch(console.error).finally(() => setLoading(false))
  }
  useEffect(load, [id])
  useEffect(() => { api.get('/users').then(d => setAllUsers(d?.users || [])) }, [])

  if (loading) return <Layout><Loading /></Layout>
  if (!data) return <Layout><Empty title="用户不存在" /></Layout>

  const { user, nodes = [], grants = [], all_nodes = [], rules = [], landing_nodes = [] } = data
  const nodeMap = Object.fromEntries(all_nodes.map(n => [n.id, n]))
  const isRegularUser = user.role === 'user'
  const expiresAt = user.expires_at && user.expires_at > 0 ? user.expires_at : null

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
      return d
    } catch (err) { toast(err.message, 'error') }
  }

  return (
    <Layout>
      <DetailHeader
        title={user.username}
        meta={`ID: ${user.id} · ${user.role}`}
        backTo="/users"
        backLabel="返回用户列表"
      />

      <UserInfoCard
        user={user}
        rules={rules}
        isRegularUser={isRegularUser}
        onToggle={toggleUser}
        onResetTraffic={resetTraffic}
        onResetPassword={resetPassword}
        onDelete={deleteUser}
        load={load}
      />

      {isRegularUser && (
        <SectionCard title="可编辑配置">
          <div className="px-6 py-[22px]">
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
        </SectionCard>
      )}

      {isRegularUser && (
        <GrantedNodesCard
          userId={id}
          nodes={nodes}
          grants={grants}
          allNodes={all_nodes}
          allUsers={allUsers}
          userSpeedLimitMBytes={user.speed_limit_mbytes || 0}
          onDone={load}
        />
      )}

      {isRegularUser && (
        <LandingSourceCard
          userId={id}
          subURL={user.landing_sub_url}
          uris={user.landing_uris}
          nodes={landing_nodes}
          blurred={blurred}
        />
      )}

      <SectionCard title="该用户的规则" subtitle={`${rules.length} 条`}>
        {rules.length ? (
          <TableBox>
          <table className="tbl">
            <thead>
              <tr>
                <th>ID</th><th>名称</th><th>节点</th><th>入口</th><th>出口</th>
                <th className="text-right">真实用量</th>
                <th className="text-right">显示用量(×{user.billing_rate ?? 1})</th>
              </tr>
            </thead>
            <tbody>
              {rules.map(r => (
                <tr key={r.id}>
                  <td className="font-mono text-xs text-ink-mut">{r.id}</td>
                  <td className="font-semibold">
                    <Link to={`/rules/${r.id}`} className="text-blue-600 hover:underline">{r.name}</Link>
                  </td>
                  <td className="font-mono text-ink-soft">{nodeMap[r.node_id]?.name || `#${r.node_id}`}</td>
                  <td className="font-mono text-xs">--</td>
                  <td className="font-mono text-xs">--</td>
                  <td className="text-right font-mono text-xs text-ink-mut">{fmtBytes(r.exit_bytes || 0)}</td>
                  <td className="text-right font-mono text-xs">{fmtBytes(Math.round((r.exit_bytes || 0) * (user.billing_rate ?? 1)))}</td>
                </tr>
              ))}
            </tbody>
          </table>
          </TableBox>
        ) : <Empty title="该用户尚无规则" />}
      </SectionCard>
    </Layout>
  )
}
