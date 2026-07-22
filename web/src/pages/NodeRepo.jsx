import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { Layout, useToast } from '../components/Layout'
import { Loading, Empty, Badge, CopyText, Modal, useConfirm, DateInput } from '../components/ui'
import { PageHeader, Panel, PanelToolbar, ToolbarButton, ToolbarActions, TableScroll, SearchInput } from '../components/page'
import FolderBar, { MoveToFolderModal } from '../components/FolderBar'
import { parseURIs, tryParseURI } from '../lib/landing'
import { fmtDate, expiryBadge, fmtTrafficGB } from '../lib/fmt'
import { useIsMobile } from '../lib/useIsMobile'

export default function NodeRepo() {
  const [list, setList] = useState(null)
  const [folders, setFolders] = useState([])
  const [ungrouped, setUngrouped] = useState(0)
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState(false)
  const [showBulk, setShowBulk] = useState(false)
  const [showMove, setShowMove] = useState(false)
  const [editing, setEditing] = useState(null)
  const [search, setSearch] = useState('')
  const [folderFilter, setFolderFilter] = useState('') // '' all | '0' ungrouped | folder id
  const [sel, setSel] = useState(new Set())
  const [usersFor, setUsersFor] = useState(null) // node entry when modal open
  const [usersList, setUsersList] = useState(null)
  const [usersLoading, setUsersLoading] = useState(false)
  const toast = useToast()
  const confirm = useConfirm()
  const isMobile = useIsMobile()

  const loadFolders = () => api.get('/node-repo-folders').then(d => {
    setFolders(d?.folders || [])
    setUngrouped(d?.ungrouped || 0)
  }).catch(() => {})

  const load = () => {
    setLoading(true)
    Promise.all([
      api.get('/node-repo').then(d => setList(d?.nodes || [])),
      loadFolders(),
    ]).catch(console.error).finally(() => setLoading(false))
  }
  useEffect(load, [])

  const deleteNode = async (n) => {
    if (!(await confirm({ title: '删除节点', message: `确认删除节点「${n.name}」？`, confirmText: '删除', danger: true }))) return
    try { await api.del(`/node-repo/${n.id}`); toast('已删除'); load() } catch (err) { toast(err.message, 'error') }
  }

  const bulkDelete = async () => {
    if (sel.size === 0) { toast('请先勾选要删除的节点', 'error'); return }
    if (!(await confirm({ title: '批量删除', message: `确认删除选中的 ${sel.size} 个节点？`, confirmText: '删除', danger: true }))) return
    try {
      for (const id of sel) { await api.del(`/node-repo/${id}`) }
      toast(`已删除 ${sel.size} 个节点`)
      setSel(new Set())
      load()
    } catch (err) { toast(err.message, 'error') }
  }

  const openUsers = async (n) => {
    setUsersFor(n)
    setUsersList(null)
    setUsersLoading(true)
    try {
      const d = await api.get(`/node-repo/${n.id}/users`)
      setUsersList(d?.users || [])
    } catch (err) {
      toast(err.message, 'error')
      setUsersFor(null)
    } finally {
      setUsersLoading(false)
    }
  }

  const folderName = (id) => folders.find(f => f.id === id)?.name || ''

  const moveToFolder = async (folderId) => {
    if (sel.size === 0) { toast('请先勾选节点', 'error'); return }
    await api.post('/node-repo/batch-group', { ids: [...sel], group_id: folderId })
    const label = folderId ? (folderName(folderId) || '分组') : '未分组'
    toast(folderId ? `已移入「${label}」` : '已移出到未分组')
    setSel(new Set())
    setShowMove(false)
    load()
  }

  const toggleSel = (id) => setSel(s => {
    const next = new Set(s)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })

  if (loading) return <Layout><Loading /></Layout>

  const q = search.trim().toLowerCase()
  let filtered = list || []
  if (folderFilter === '0') filtered = filtered.filter(n => !(n.group_id > 0))
  else if (folderFilter) filtered = filtered.filter(n => String(n.group_id) === String(folderFilter))
  if (q) filtered = filtered.filter(n =>
    [n.name, n.protocol, `${n.host}:${n.port}`, n.remark, n.group_name].some(v => (v || '').toLowerCase().includes(q)))

  const toggleSelAll = () => setSel(s =>
    s.size === filtered.length && filtered.length > 0 ? new Set() : new Set(filtered.map(n => n.id)))

  return (
    <Layout>
      <div className="h-full flex flex-col">
      <PageHeader title="落地仓库" count={list?.length || 0} unit="个" />
      <Panel fill>
        <div className="px-3 pt-2 border-b border-line">
          <FolderBar
            folders={folders}
            ungrouped={ungrouped}
            total={(list || []).length}
            filter={folderFilter}
            onFilter={f => { setFolderFilter(f); setSel(new Set()) }}
            onCreate={async (name) => { await api.post('/node-repo-folders', { name }); await loadFolders() }}
            onRename={async (id, name) => { await api.patch(`/node-repo-folders/${id}`, { name }); await loadFolders(); load() }}
            onDelete={async (id) => { await api.del(`/node-repo-folders/${id}`); await loadFolders(); load() }}
          />
        </div>
        <PanelToolbar>
          <SearchInput value={search} onChange={setSearch} placeholder="搜索名称、协议、地址…" />
          {sel.size > 0 && (
            <>
              <button onClick={() => setShowMove(true)} className="text-emerald-600 text-xs font-semibold px-3 py-1 rounded border border-emerald-200 hover:bg-emerald-50 dark:border-emerald-700 dark:hover:bg-emerald-900/20">移入分组 ({sel.size})</button>
              <button onClick={bulkDelete} className="text-red-600 text-xs font-semibold px-3 py-1 rounded border border-red-200 hover:bg-red-50 dark:border-red-700 dark:hover:bg-red-900/20">删除选中 {sel.size}</button>
            </>
          )}
          <ToolbarActions>
            <ToolbarButton onClick={() => setShowBulk(true)} secondary>批量导入</ToolbarButton>
            <ToolbarButton onClick={() => { setEditing(null); setShowForm(true) }}>＋ 添加节点</ToolbarButton>
          </ToolbarActions>
        </PanelToolbar>

        <TableScroll>
        {!list || list.length === 0 ? (
          <Empty title="暂无节点" desc="点击右上角「添加节点」将预先准备好的代理节点录入落地仓库。" />
        ) : filtered.length === 0 ? (
          <Empty title="无匹配节点" desc="试试别的关键词或分组。" />
        ) : (<>
          {!isMobile && <table className="tbl">
            <thead><tr>
              <th className="w-8"><input type="checkbox" className="accent-emerald-600"
                checked={filtered.length > 0 && sel.size === filtered.length} onChange={toggleSelAll} /></th>
              <th>名称</th><th>分组</th><th>协议</th><th>地址</th><th>使用</th><th>到期时间</th><th>备注</th><th>创建时间</th><th className="text-right">操作</th></tr></thead>
            <tbody>
              {filtered.map(n => (
                <tr key={n.id}>
                  <td><input type="checkbox" className="accent-emerald-600" checked={sel.has(n.id)} onChange={() => toggleSel(n.id)} /></td>
                  <td className="font-semibold">{n.name}</td>
                  <td className="text-xs">{n.group_name ? <Badge color="blue">{n.group_name}</Badge> : <span className="text-ink-mut">—</span>}</td>
                  <td className="font-mono text-xs text-ink-soft">{n.protocol || '—'}</td>
                  <td className="font-mono text-xs">{n.host}:{n.port}</td>
                  <td className="text-xs">
                    {(n.user_count || 0) > 0 ? (
                      <button type="button" onClick={() => openUsers(n)}
                        className="inline-flex items-center px-2 py-0.5 rounded-full text-[11.5px] font-semibold border bg-emerald-50 text-emerald-700 border-emerald-200 hover:bg-emerald-100 dark:bg-emerald-900/30 dark:text-emerald-400 dark:border-emerald-700 transition-colors"
                        title="查看使用此落地的用户">
                        {n.user_count} 人
                      </button>
                    ) : (
                      <span className="text-ink-mut">—</span>
                    )}
                  </td>
                  <td className="text-xs">
                    {n.expires_at > 0 ? (
                      <span className="inline-flex items-center gap-1.5">{fmtDate(n.expires_at)}{(() => { const b = expiryBadge(n.expires_at); return b ? <Badge color={b.color}>{b.label}</Badge> : null })()}</span>
                    ) : <span className="text-ink-mut">—</span>}
                  </td>
                  <td className="text-xs text-ink-soft">{n.remark || '—'}</td>
                  <td className="text-xs text-ink-mut">{new Date(n.created_at * 1000).toLocaleDateString('zh-CN')}</td>
                  <td className="text-right">
                    <div className="inline-flex items-center gap-3">
                      {n.uri && <CopyText text={n.uri}><span className="text-emerald-600 text-xs font-semibold hover:underline cursor-pointer">复制</span></CopyText>}
                      <button onClick={() => { setEditing(n); setShowForm(true) }} className="text-emerald-600 text-xs font-semibold hover:underline">编辑</button>
                      <button onClick={() => deleteNode(n)} className="text-red-600 text-xs font-semibold hover:underline">删除</button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>}
          {isMobile && <div>
            {filtered.map(n => (
              <div key={n.id} className="mobile-card">
                <div className="flex items-center justify-between mb-1">
                  <label className="flex items-center gap-2 font-semibold">
                    <input type="checkbox" className="accent-emerald-600" checked={sel.has(n.id)} onChange={() => toggleSel(n.id)} />
                    {n.name}
                  </label>
                  <span className="inline-flex items-center gap-2">
                    {n.uri && <CopyText text={n.uri}><span className="text-emerald-600 text-xs font-semibold">复制</span></CopyText>}
                    <button onClick={() => { setEditing(n); setShowForm(true) }} className="text-emerald-600 text-xs font-semibold">编辑</button>
                    <button onClick={() => deleteNode(n)} className="text-red-600 text-xs font-semibold">删除</button>
                  </span>
                </div>
                <div className="flex items-center gap-2 text-xs text-ink-soft flex-wrap">
                  {n.group_name && <Badge color="blue">{n.group_name}</Badge>}
                  <span className="font-mono">{n.protocol || '—'}</span>
                  <span className="text-ink-mut">·</span>
                  <span className="font-mono">{n.host}:{n.port}</span>
                  {(n.user_count || 0) > 0 ? (
                    <button type="button" onClick={() => openUsers(n)}
                      className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-semibold border bg-emerald-50 text-emerald-700 border-emerald-200">
                      {n.user_count} 人
                    </button>
                  ) : (
                    <span className="text-ink-mut">未使用</span>
                  )}
                  {n.expires_at > 0 && <>
                    <span className="text-ink-mut">·</span>
                    <span>{fmtDate(n.expires_at)}{(() => { const b = expiryBadge(n.expires_at); return b ? <Badge color={b.color}>{b.label}</Badge> : null })()}</span>
                  </>}
                  {n.remark && <>
                    <span className="text-ink-mut">·</span>
                    <span>{n.remark}</span>
                  </>}
                </div>
              </div>
            ))}
          </div>}
        </>)}
        </TableScroll>
      </Panel>
      </div>

      {showForm && (
        <NodeRepoForm
          node={editing}
          folders={folders}
          onClose={() => setShowForm(false)}
          onDone={() => { setShowForm(false); load() }}
        />
      )}

      {usersFor && (
        <Modal open onClose={() => setUsersFor(null)} title={`${usersFor.name} · ${usersFor.host}:${usersFor.port}`} wide>
          <div className="space-y-3">
            <p className="text-[13px] text-ink-soft">
              {usersLoading ? '加载中…' : `${(usersList || []).length} 个用户正在使用此落地`}
            </p>
            {usersLoading ? (
              <div className="py-8 text-center text-ink-mut text-sm">加载中…</div>
            ) : !usersList?.length ? (
              <div className="py-8 text-center text-ink-mut text-sm">暂无用户使用</div>
            ) : (
              <div className="overflow-x-auto max-h-[60vh] overflow-y-auto border border-line rounded-xl">
                <table className="tbl">
                  <thead>
                    <tr>
                      <th>用户</th>
                      <th>落地显示名</th>
                      <th>流量</th>
                      <th>规则</th>
                    </tr>
                  </thead>
                  <tbody>
                    {usersList.map(u => (
                      <tr key={u.user_id}>
                        <td className="font-semibold">
                          <Link to={`/users/${u.user_id}`} className="text-emerald-600 hover:underline" onClick={() => setUsersFor(null)}>
                            {u.username}
                          </Link>
                        </td>
                        <td className="text-xs text-ink-soft">{u.name_override || u.name || '—'}</td>
                        <td className="text-xs font-mono text-ink-soft">
                          {(u.quota_bytes > 0 || u.used_bytes > 0)
                            ? fmtTrafficGB(u.used_bytes, u.quota_bytes)
                            : '—'}
                        </td>
                        <td className="text-xs">{u.rule_count > 0 ? `${u.rule_count} 条` : <span className="text-ink-mut">0</span>}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <div className="flex justify-end pt-1">
              <button type="button" onClick={() => setUsersFor(null)}
                className="px-4 py-2 text-[13px] font-semibold rounded-xl border border-line bg-surface hover:bg-raised">
                关闭
              </button>
            </div>
          </div>
        </Modal>
      )}

      {showBulk && (
        <BulkImportForm
          folders={folders}
          onClose={() => setShowBulk(false)}
          onDone={() => { setShowBulk(false); load() }}
        />
      )}
      {showMove && (
        <MoveToFolderModal
          title={`将 ${sel.size} 个节点移入…`}
          folders={folders}
          onClose={() => setShowMove(false)}
          onMove={moveToFolder}
        />
      )}
    </Layout>
  )
}

function NodeRepoForm({ node, folders = [], onClose, onDone }) {
  const isEdit = !!node
  const [form, setForm] = useState({
    name: node?.name || '',
    protocol: node?.protocol || '',
    host: node?.host || '',
    port: node?.port || '',
    uri: node?.uri || '',
    remark: node?.remark || '',
    group_id: node?.group_id > 0 ? String(node.group_id) : '0',
    expires_at: node?.expires_at > 0 ? new Date(node.expires_at * 1000).toISOString().slice(0, 10) : '',
  })
  const [submitting, setSubmitting] = useState(false)
  const toast = useToast()

  const set = (k, v) => setForm(f => ({ ...f, [k]: v }))

  // Auto-parse URI: when user pastes a proxy URI into the URI field,
  // extract protocol, host, port, and name automatically.
  const handleURIBlur = () => {
    const uri = form.uri.trim()
    if (!uri || !uri.includes('://')) return
    const parsed = tryParseURI(uri)
    if (!parsed) return
    setForm(f => ({
      ...f,
      protocol: parsed.protocol || f.protocol,
      host: parsed.host || f.host,
      port: parsed.port || f.port,
      name: !f.name.trim() && parsed.name ? parsed.name : f.name,
    }))
    toast(`已识别 ${parsed.protocol} 节点：${parsed.host}:${parsed.port}`)
  }

  const submit = async (e) => {
    e.preventDefault()
    if (!form.name.trim() || !form.host.trim() || !form.port) { toast('名称、地址、端口不能为空', 'error'); return }
    setSubmitting(true)
    try {
      const gid = Number(form.group_id) || 0
      const body = {
        name: form.name.trim(),
        protocol: form.protocol.trim(),
        host: form.host.trim(),
        port: Number(form.port),
        uri: form.uri.trim(),
        remark: form.remark.trim(),
        group_id: gid,
        group_name: '',
        expires_at: form.expires_at ? Math.floor(new Date(form.expires_at).getTime() / 1000) : 0,
      }
      if (isEdit) {
        await api.patch(`/node-repo/${node.id}`, body)
        toast('已更新')
      } else {
        await api.post('/node-repo', body)
        toast('已添加')
      }
      onDone()
    } catch (err) {
      toast(err.message, 'error')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑节点' : '添加节点'}>
      <form onSubmit={submit} className="space-y-4">
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">URI <span className="text-ink-mut font-normal text-xs">(粘贴后自动识别)</span></label>
            <input className="input-field font-mono text-xs" value={form.uri} onChange={e => set('uri', e.target.value)} onBlur={handleURIBlur} placeholder="粘贴代理 URI，自动填充下方字段" />
          </div>
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">名称</label>
            <input className="input-field" value={form.name} onChange={e => set('name', e.target.value)} placeholder="自定义节点名称" autoFocus />
          </div>
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">分组</label>
            <select className="input-field" value={form.group_id} onChange={e => set('group_id', e.target.value)}>
              <option value="0">未分组</option>
              {folders.map(f => <option key={f.id} value={String(f.id)}>{f.name}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">协议</label>
            <input className="input-field" value={form.protocol} onChange={e => set('protocol', e.target.value)} placeholder="如 ss, vmess, trojan" />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">地址</label>
              <input className="input-field font-mono" value={form.host} onChange={e => set('host', e.target.value)} placeholder="IP 或域名" />
            </div>
            <div>
              <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">端口</label>
              <input className="input-field font-mono" type="number" min="1" max="65535" value={form.port} onChange={e => set('port', e.target.value)} placeholder="端口" />
            </div>
          </div>
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">备注 <span className="text-ink-mut font-normal text-xs">(可选)</span></label>
            <input className="input-field" value={form.remark} onChange={e => set('remark', e.target.value)} placeholder="备注" />
          </div>
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">到期时间 <span className="text-ink-mut font-normal text-xs">(可选，留空永不过期)</span></label>
            {/* Keep date near the bottom actions but with room so the native
                calendar can open upward without sitting under the footer. */}
            <DateInput
              value={form.expires_at}
              onChange={v => set('expires_at', v)}
              className="w-full"
              placeholder="留空永不过期"
            />
          </div>
          <div className="flex gap-2 pt-2">
            <button type="submit" disabled={submitting} className="btn-primary flex-1">{submitting ? '保存中…' : '保存'}</button>
            <button type="button" onClick={onClose} className="btn-secondary flex-1">取消</button>
          </div>
        </form>
    </Modal>
  )
}

function BulkImportForm({ folders = [], onClose, onDone }) {
  const [text, setText] = useState('')
  const [groupId, setGroupId] = useState('0')
  const [submitting, setSubmitting] = useState(false)
  const toast = useToast()

  const submit = async (e) => {
    e.preventDefault()
    const lines = text.split('\n').map(l => l.trim()).filter(Boolean)
    if (lines.length === 0) { toast('请输入至少一条 URI', 'error'); return }
    const nodes = parseURIs(lines.join('\n'))
    if (nodes.length === 0) { toast('未能解析出任何有效节点', 'error'); return }
    setSubmitting(true)
    try {
      let count = 0
      const gid = Number(groupId) || 0
      for (const n of nodes) {
        await api.post('/node-repo', {
          name: n.name || '(未命名)',
          protocol: n.protocol || '',
          host: n.host,
          port: n.port,
          uri: n.uri || '',
          remark: '',
          group_id: gid,
          group_name: '',
        })
        count++
      }
      toast(`成功导入 ${count} 个节点`)
      onDone()
    } catch (err) {
      toast(err.message, 'error')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal open onClose={onClose} title="批量导入节点" wide>
      <form onSubmit={submit} className="space-y-4">
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">节点 URI（每行一条）</label>
            <textarea className="input-field font-mono text-xs" value={text} onChange={e => setText(e.target.value)}
              placeholder={'ss://…\nvmess://…\ntrojan://…\nvless://…'} rows={16} style={{ resize: 'vertical', minHeight: 300 }} autoFocus />
          </div>
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">导入到分组</label>
            <select className="input-field" value={groupId} onChange={e => setGroupId(e.target.value)}>
              <option value="0">未分组</option>
              {folders.map(f => <option key={f.id} value={String(f.id)}>{f.name}</option>)}
            </select>
          </div>
          <div className="flex gap-2 pt-2">
            <button type="submit" disabled={submitting} className="btn-primary flex-1">{submitting ? '导入中…' : '导入'}</button>
            <button type="button" onClick={onClose} className="btn-secondary flex-1">取消</button>
          </div>
        </form>
    </Modal>
  )
}
