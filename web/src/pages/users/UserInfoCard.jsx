import { useState } from 'react'
import { fmtTrafficGB, pct, fmtDate, expiryBadge, nullStr } from '../../lib/fmt'
import { useToast } from '../../components/Layout'
import { Badge, Modal } from '../../components/ui'
import { copyToClipboard } from '../../lib/clipboard'
import { InfoGrid } from '../../components/page'

/**
 * UserInfoCard — 用户基本信息 + 操作按钮
 */
export default function UserInfoCard({ user, rules, isRegularUser, onToggle, onResetTraffic, onResetPassword, onDelete }) {
  const toast = useToast()
  const [newPassword, setNewPassword] = useState(null)

  const expiresAt = user.expires_at && user.expires_at > 0 ? user.expires_at : null

  const infoItems = isRegularUser
    ? [
        { label: '用户名', value: user.username, accent: true },
        { label: '角色', value: <span className="font-mono">{user.role}</span> },
        { label: '规则配额', value: <span className="font-mono">{rules.length} / {user.max_forwards}</span> },
        { label: '流量', value: (
            <span className="font-mono">
              {fmtTrafficGB(user.traffic_used_bytes, user.traffic_quota_bytes)}
              {user.traffic_quota_bytes > 0 && ` (${pct(user.traffic_used_bytes, user.traffic_quota_bytes)}%)`}
              {user.traffic_reset_days > 0 && <span className="text-ink-mut text-xs ml-1">每{user.traffic_reset_days}天重置</span>}
            </span>
          )},
        { label: '用户视角流量', value: (
            <span className="font-mono">
              {fmtTrafficGB((user.traffic_used_bytes || 0) * (user.billing_rate ?? 1), user.traffic_quota_bytes)}
              {user.traffic_quota_bytes > 0 && ` (${pct((user.traffic_used_bytes || 0) * (user.billing_rate ?? 1), user.traffic_quota_bytes)}%)`}
              <span className="text-ink-mut text-xs ml-1">已用×{user.billing_rate ?? 1}</span>
            </span>
          )},
        { label: '计费倍率', value: <span className="font-mono">×{user.billing_rate ?? 1}</span> },
        { label: '到期时间', value: (
            <span className="font-mono">
              {expiresAt ? <>{fmtDate(expiresAt)}{(() => { const b = expiryBadge(expiresAt); return b ? <Badge color={b.color} className="ml-1">{b.label}</Badge> : null })()}</> : '永不过期'}
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

  const handleResetPassword = async () => {
    const res = await onResetPassword()
    if (res?.new_password) setNewPassword(res.new_password)
  }

  return (
    <>
      <div className="card mb-5 soft-panel">
        <div className="card-header"><h3 className="text-[15px] font-bold">基本信息</h3></div>
        <div className="px-6 py-[22px] section-surface">
          <InfoGrid items={infoItems} />

          {isRegularUser && (
            <div className="flex items-center gap-2 mt-5 flex-wrap">
              <button onClick={onToggle} className="btn-secondary text-xs">{user.disabled ? '启用' : '禁用'}</button>
              <button onClick={onResetTraffic} className="btn-secondary text-xs">重置流量</button>
              <button onClick={handleResetPassword} className="btn-secondary text-xs">重置密码</button>
              <button onClick={onDelete} className="btn-secondary text-xs">删除用户</button>
            </div>
          )}
        </div>
      </div>

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
    </>
  )
}