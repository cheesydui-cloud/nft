import { useState, useEffect } from 'react'
import { api } from '../lib/api'
import { Layout, useToast } from '../components/Layout'
import { Loading, Empty, useConfirm } from '../components/ui'
import { PageHeader, Panel, PanelToolbar, ToolbarButton, TableScroll } from '../components/page'
import { parseURIs } from '../lib/landing'

export default function NodeRepo() {
  const [list, setList] = useState(null)
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState(false)
  const [showBulk, setShowBulk] = useState(false)
  const [editing, setEditing] = useState(null)
  const toast = useToast()
  const confirm = useConfirm()

  const load = () => {
    setLoading(true)
    api.get('/node-repo').then(d => setList(d?.nodes || [])).catch(console.error).finally(() => setLoading(false))
  }
  useEffect(load, [])

  const deleteNode = async (n) => {
    if (!(await confirm({ title: '删除节点', message: `确认删除节点「${n.name}」？`, confirmText: '删除', danger: true }))) return
    try { await api.del(`/node-repo/${n.id}`); toast('已删除'); load() } catch (err) { toast(err.message, 'error') }
  }

  if (loading) return <Layout><Loading /></Layout>

  return (
    <Layout>
      <div className="h-full flex flex-col">
      <PageHeader title="节点池" count={list?.length || 0} unit="个" />
      <Panel fill>
        <PanelToolbar>
          <div className="ml-auto flex items-center gap-2">
            <ToolbarButton onClick={() => setShowBulk(true)} secondary>批量导入</ToolbarButton>
            <ToolbarButton onClick={() => { setEditing(null); setShowForm(true) }}>＋ 添加节点</ToolbarButton>
          </div>
        </PanelToolbar>

        <TableScroll>
        {!list || list.length === 0 ? (
          <Empty title="暂无节点" desc="点击右上角「添加节点」将预先准备好的代理节点录入节点池。" />
        ) : (
          <table className="tbl">
            <thead><tr><th>名称</th><th>协议</th><th>地址</th><th>备注</th><th>创建时间</th><th className="text-right">操作</th></tr></thead>
            <tbody>
              {list.map(n => (
                <tr key={n.id}>
                  <td className="font-semibold">{n.name}</td>
                  <td className="font-mono text-xs text-ink-soft">{n.protocol || '—'}</td>
                  <td className="font-mono text-xs">{n.host}:{n.port}</td>
                  <td className="text-xs text-ink-soft">{n.remark || '—'}</td>
                  <td className="text-xs text-ink-mut">{new Date(n.created_at * 1000).toLocaleDateString('zh-CN')}</td>
                  <td className="text-right whitespace-nowrap">
                    <button onClick={() => { setEditing(n); setShowForm(true) }} className="text-blue-600 text-xs font-semibold hover:underline mr-3">编辑</button>
                    <button onClick={() => deleteNode(n)} className="text-red-600 text-xs font-semibold hover:underline">删除</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        </TableScroll>
      </Panel>
      </div>

      {showForm && (
        <NodeRepoForm
          node={editing}
          onClose={() => setShowForm(false)}
          onDone={() => { setShowForm(false); load() }}
        />
      )}
      {showBulk && (
        <BulkImportForm
          onClose={() => setShowBulk(false)}
          onDone={() => { setShowBulk(false); load() }}
        />
      )}
    </Layout>
  )
}

function NodeRepoForm({ node, onClose, onDone }) {
  const isEdit = !!node
  const [form, setForm] = useState({
    name: node?.name || '',
    protocol: node?.protocol || '',
    host: node?.host || '',
    port: node?.port || '',
    uri: node?.uri || '',
    remark: node?.remark || '',
  })
  const [submitting, setSubmitting] = useState(false)
  const toast = useToast()

  const set = (k, v) => setForm(f => ({ ...f, [k]: v }))

  const submit = async (e) => {
    e.preventDefault()
    if (!form.name.trim() || !form.host.trim() || !form.port) { toast('名称、地址、端口不能为空', 'error'); return }
    setSubmitting(true)
    try {
      const body = {
        name: form.name.trim(),
        protocol: form.protocol.trim(),
        host: form.host.trim(),
        port: Number(form.port),
        uri: form.uri.trim(),
        remark: form.remark.trim(),
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
    <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40" onClick={onClose}>
      <div className="bg-surface rounded-xl shadow-2xl border border-line w-full max-w-lg mx-4" onClick={e => e.stopPropagation()}>
        <div className="px-6 py-4 border-b border-line-soft">
          <h3 className="text-[16px] font-bold">{isEdit ? '编辑节点' : '添加节点'}</h3>
        </div>
        <form onSubmit={submit} className="px-6 py-5 space-y-4">
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">名称</label>
            <input className="input-field" value={form.name} onChange={e => set('name', e.target.value)} placeholder="自定义节点名称" autoFocus />
          </div>
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">协议 <span className="text-ink-mut font-normal text-xs">(可选)</span></label>
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
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">URI <span className="text-ink-mut font-normal text-xs">(可选)</span></label>
            <input className="input-field font-mono text-xs" value={form.uri} onChange={e => set('uri', e.target.value)} placeholder="完整代理 URI（用于用户端连接）" />
          </div>
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">备注 <span className="text-ink-mut font-normal text-xs">(可选)</span></label>
            <input className="input-field" value={form.remark} onChange={e => set('remark', e.target.value)} placeholder="备注" />
          </div>
          <div className="flex gap-2 pt-2">
            <button type="submit" disabled={submitting} className="btn-primary flex-1">{submitting ? '保存中…' : '保存'}</button>
            <button type="button" onClick={onClose} className="btn-secondary flex-1">取消</button>
          </div>
        </form>
      </div>
    </div>
  )
}

function BulkImportForm({ onClose, onDone }) {
  const [text, setText] = useState('')
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
      for (const n of nodes) {
        await api.post('/node-repo', {
          name: n.name || '(未命名)',
          protocol: n.protocol || '',
          host: n.host,
          port: n.port,
          uri: n.uri || '',
          remark: '',
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
    <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40" onClick={onClose}>
      <div className="bg-surface rounded-xl shadow-2xl border border-line w-full max-w-lg mx-4" onClick={e => e.stopPropagation()}>
        <div className="px-6 py-4 border-b border-line-soft">
          <h3 className="text-[16px] font-bold">批量导入节点</h3>
        </div>
        <form onSubmit={submit} className="px-6 py-5 space-y-4">
          <div>
            <label className="block text-[13px] font-semibold text-ink-soft mb-1.5">节点 URI（每行一条）</label>
            <textarea className="input-field font-mono text-xs" value={text} onChange={e => setText(e.target.value)}
              placeholder={'ss://…\nvmess://…\ntrojan://…'} rows={10} style={{ resize: 'vertical' }} autoFocus />
          </div>
          <div className="flex gap-2 pt-2">
            <button type="submit" disabled={submitting} className="btn-primary flex-1">{submitting ? '导入中…' : '导入'}</button>
            <button type="button" onClick={onClose} className="btn-secondary flex-1">取消</button>
          </div>
        </form>
      </div>
    </div>
  )
}
