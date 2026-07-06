import { useState, useEffect } from 'react'
import { api } from '../lib/api'
import { Layout, useToast } from '../components/Layout'
import { Loading, Empty, Badge, useConfirm } from '../components/ui'
import { PageHeader, Panel, PanelToolbar, ToolbarButton, TableScroll } from '../components/page'
import { fmtDate, isExpired } from '../lib/fmt'

export default function Announcements() {
  const [list, setList] = useState(null)
  const [users, setUsers] = useState([])
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState(false)
  const toast = useToast()
  const confirm = useConfirm()

  const load = () => {
    setLoading(true)
    Promise.all([
      api.get('/announcements').then(d => d?.announcements || []).catch(() => []),
      api.get('/users').then(d => d?.users || []).catch(() => []),
    ]).then(([a, u]) => {
      setList(a)
      setUsers(u)
    }).finally(() => setLoading(false))
  }
  useEffect(load, [])

  const deleteAnn = async (ann) => {
    if (!(await confirm({ title: '删除公告', message: `确认删除公告「${ann.title}」？`, confirmText: '删除', danger: true }))) return
    try { await api.del(`/announcements/${ann.id}`); toast('已删除'); load() } catch (err) { toast(err.message, 'error') }
  }

  if (loading) return <Layout><Loading /></Layout>

  return (
    <Layout>
      <div className="h-full flex flex-col">
      <PageHeader title="公告管理" count={list?.length || 0} unit="条" />
      <Panel fill>
        <PanelToolbar>
          <div className="ml-auto">
            <ToolbarButton onClick={() => setShowForm(true)}>＋ 发布公告</ToolbarButton>
          </div>
        </PanelToolbar>

        <TableScroll>
        {!list || list.length === 0 ? (
          <Empty title="暂无公告" desc="点击右上角「发布公告」创建第一条公告。" />
        ) : (
          <table className="tbl">
            <thead><tr><th>标题</th><th>内容</th><th>推送对象</th><th>创建时间</th><th>到期时间</th><th className="text-right">操作</th></tr></thead>
            <tbody>
              {list.map(a => {
                const target = a.target_user_id === 0 ? '所有用户' : users.find(u => u.id === a.target_user_id)?.username || `用户#${a.target_user_id}`
                return (
                  <tr key={a.id}>
                    <td className="font-semibold">{a.title}</td>
                    <td className="text-xs text-ink-soft max-w-[300px] truncate">{a.content}</td>
                    <td>
                      {a.target_user_id === 0
                        ? <Badge color="green">所有用户</Badge>
                        : <Badge color="blue">{target}</Badge>}
                    </td>
                    <td className="text-xs text-ink-mut">{fmtDate(a.created_at)}</td>
                    <td className="text-xs text-ink-mut">
                      {a.expires_at > 0
                        ? <>{fmtDate(a.expires_at)}{isExpired(a.expires_at) && <Badge color="red" className="ml-1">已过期</Badge>}</>
                        : '永不'}
                    </td>
                    <td className="text-right">
                      <button onClick={() => deleteAnn(a)} className="text-red-600 text-xs font-semibold hover:underline">删除</button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
        </TableScroll>
      </Panel>
      </div>

      {showForm && (
        <AnnouncementForm
          users={users}
          onClose={() => setShowForm(false)}
          onDone={() => { setShowForm(false); load() }}
        />
      )}
    </Layout>
  )
}

function AnnouncementForm({ users, onClose, onDone }) {
  const [title, setTitle] = useState('')
  const [content, setContent] = useState('')
  const [targetMode, setTargetMode] = useState('all') // 'all' or 'user'
  const [targetUserId, setTargetUserId] = useState('')
  const [hasExpiry, setHasExpiry] = useState(false)
  const [expiryDate, setExpiryDate] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const toast = useToast()

  const submit = async (e) => {
    e.preventDefault()
    if (!title.trim() || !content.trim()) { toast('标题和内容不能为空', 'error'); return }
    setSubmitting(true)
    try {
      let expiresAt = 0
      if (hasExpiry && expiryDate) {
        expiresAt = Math.floor(new Date(expiryDate).getTime() / 1000)
      }
      const body = {
        title: title.trim(),
        content: content.trim(),
        target_user_id: targetMode === 'all' ? 0 : Number(targetUserId),
        expires_at: expiresAt,
      }
      await api.post('/announcements', body)
      toast('公告已发布')
      onDone()
    } catch (err) {
      toast(err.message, 'error')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40" onClick={onClose}>
      <div className="bg-surface rounded-xl shadow-2xl border border-line w-full max-w-lg mx-4" onClick={e => e.stopPropagation()}>
        <div className="px-6 py-4 border-b border-line-soft">
          <h3 className="text-[16px] font-bold">发布公告</h3>
        </div>
        <form onSubmit={submit} className="px-6 py-5 space-y-4">
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">标题</label>
            <input className="input-field" value={title} onChange={e => setTitle(e.target.value)} placeholder="输入公告标题" autoFocus />
          </div>
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">内容</label>
            <textarea className="input-field" value={content} onChange={e => setContent(e.target.value)} placeholder="输入公告内容" rows={4} style={{ resize: 'vertical' }} />
          </div>
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">推送对象</label>
            <div className="flex items-center gap-3 mb-2">
              <label className="flex items-center gap-1.5 text-sm cursor-pointer">
                <input type="radio" checked={targetMode === 'all'} onChange={() => setTargetMode('all')} />
                所有用户
              </label>
              <label className="flex items-center gap-1.5 text-sm cursor-pointer">
                <input type="radio" checked={targetMode === 'user'} onChange={() => setTargetMode('user')} />
                指定用户
              </label>
            </div>
            {targetMode === 'user' && (
              <select className="input-field" value={targetUserId} onChange={e => setTargetUserId(e.target.value)}>
                <option value="">选择用户…</option>
                {users.map(u => <option key={u.id} value={u.id}>{u.username}</option>)}
              </select>
            )}
          </div>
          <div>
            <label className="flex items-center gap-2 text-[13px] font-semibold text-ink-soft mb-1.5 cursor-pointer">
              <input type="checkbox" checked={hasExpiry} onChange={e => setHasExpiry(e.target.checked)} />
              设置到期时间
            </label>
            {hasExpiry && (
              <input type="date" className="input-field" value={expiryDate} onChange={e => setExpiryDate(e.target.value)} />
            )}
          </div>
          <div className="flex gap-2 pt-2">
            <button type="submit" disabled={submitting} className="btn-primary flex-1">{submitting ? '发布中…' : '发布'}</button>
            <button type="button" onClick={onClose} className="btn-secondary flex-1">取消</button>
          </div>
        </form>
      </div>
    </div>
  )
}
