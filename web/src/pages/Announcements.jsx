import { useState, useEffect } from 'react'
import { api } from '../lib/api'
import { Layout, useToast } from '../components/Layout'
import { Loading, Empty, Badge, useConfirm } from '../components/ui'
import { PageHeader, Panel, PanelToolbar, ToolbarButton, ToolbarActions, TableScroll } from '../components/page'
import { fmtDate, isExpired } from '../lib/fmt'

export default function Announcements() {
  const [list, setList] = useState(null)
  const [users, setUsers] = useState([])
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState(false)
  const toast = useToast()
  const confirm = useConfirm()

  const userMap = {}
  users.forEach(u => { userMap[u.id] = u.username })

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

  // Parse target display text
  const targetText = (a) => {
    if (a.target_user_ids) {
      try {
        const ids = JSON.parse(a.target_user_ids)
        if (Array.isArray(ids) && ids.length > 0) {
          return ids.map(id => userMap[id] || `用户#${id}`).join(', ')
        }
      } catch {}
    }
    return a.target_user_id === 0 ? '所有用户' : (userMap[a.target_user_id] || `用户#${a.target_user_id}`)
  }

  if (loading) return <Layout><Loading /></Layout>

  return (
    <Layout>
      <div className="h-full flex flex-col">
      <PageHeader title="公告管理" count={list?.length || 0} unit="条" />
      <Panel fill>
        <PanelToolbar>
          <ToolbarActions>
            <ToolbarButton onClick={() => setShowForm(true)}>＋ 发布公告</ToolbarButton>
          </ToolbarActions>
        </PanelToolbar>

        <TableScroll>
        {!list || list.length === 0 ? (
          <Empty title="暂无公告" desc="点击右上角「发布公告」创建第一条公告。" />
        ) : (
          <table className="tbl">
            <thead><tr><th>标题</th><th>内容</th><th>推送对象</th><th>创建时间</th><th>到期时间</th><th className="text-right">操作</th></tr></thead>
            <tbody>
              {list.map(a => {
                const isAll = !a.target_user_ids && a.target_user_id === 0
                return (
                  <tr key={a.id}>
                    <td className="font-semibold">{a.title}</td>
                    <td className="text-xs text-ink-soft max-w-[300px] truncate">{a.content}</td>
                    <td>
                      {isAll
                        ? <Badge color="green">所有用户</Badge>
                        : <Badge color="blue">{targetText(a)}</Badge>}
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
  const [targetMode, setTargetMode] = useState('all') // 'all' or 'select'
  const [selectedIds, setSelectedIds] = useState([])
  const [search, setSearch] = useState('')
  const [hasExpiry, setHasExpiry] = useState(false)
  const [expiryDate, setExpiryDate] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const toast = useToast()

  const toggleUser = (id) => {
    setSelectedIds(prev => prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id])
  }

  const filteredUsers = search.trim()
    ? users.filter(u => u.username.toLowerCase().includes(search.trim().toLowerCase()))
    : users

  const submit = async (e) => {
    e.preventDefault()
    if (!title.trim() || !content.trim()) { toast('标题和内容不能为空', 'error'); return }
    if (targetMode === 'select' && selectedIds.length === 0) { toast('请至少选择一个用户', 'error'); return }
    setSubmitting(true)
    try {
      let expiresAt = 0
      if (hasExpiry && expiryDate) {
        expiresAt = Math.floor(new Date(expiryDate).getTime() / 1000)
      }
      const body = {
        title: title.trim(),
        content: content.trim(),
        target_user_id: 0,
        target_user_ids: targetMode === 'select' ? selectedIds : [],
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
      <div className="bg-surface rounded-xl shadow-2xl border border-line w-full max-w-lg mx-4 max-h-[90vh] overflow-y-auto" onClick={e => e.stopPropagation()}>
        <div className="px-6 py-4 border-b border-line-soft sticky top-0 bg-surface z-10">
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
                <input type="radio" checked={targetMode === 'select'} onChange={() => setTargetMode('select')} />
                多选用户
              </label>
              {targetMode === 'select' && selectedIds.length > 0 && (
                <span className="text-xs text-blue-600 font-semibold">已选 {selectedIds.length} 人</span>
              )}
            </div>
            {targetMode === 'select' && (
              <div className="border border-line rounded-lg p-3 max-h-[200px] overflow-y-auto">
                <div className="flex items-center gap-2 mb-2">
                  <input className="input-field flex-1" placeholder="搜索用户名…" value={search} onChange={e => setSearch(e.target.value)} />
                  <button type="button" onClick={() => setSelectedIds(filteredUsers.map(u => u.id))} className="text-xs text-blue-600 font-semibold px-2 py-1 hover:underline whitespace-nowrap">全选</button>
                  <button type="button" onClick={() => setSelectedIds([])} className="text-xs text-ink-mut font-semibold px-2 py-1 hover:underline whitespace-nowrap">清空</button>
                </div>
                <div className="grid grid-cols-2 gap-1.5">
                  {filteredUsers.map(u => (
                    <label key={u.id} className="flex items-center gap-2 text-sm cursor-pointer py-1 px-1.5 rounded hover:bg-raised transition-colors">
                      <input type="checkbox" checked={selectedIds.includes(u.id)} onChange={() => toggleUser(u.id)} />
                      <span className="truncate">{u.username}</span>
                    </label>
                  ))}
                </div>
                {filteredUsers.length === 0 && <div className="text-xs text-ink-mut text-center py-2">无匹配用户</div>}
              </div>
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
