import { useState, useEffect, useRef, useCallback } from 'react'
import { api } from '../lib/api'
import { Layout, useToast } from '../components/Layout'
import { Loading, Empty, Badge, useConfirm } from '../components/ui'
import { PageHeader, Panel, PanelToolbar, ToolbarButton, ToolbarActions, TableScroll } from '../components/page'
import { Markdown } from '../lib/markdown'
import { fmtDate } from '../lib/fmt'

async function uploadDocImage(file) {
  const fd = new FormData()
  fd.append('file', file)
  let res
  try {
    res = await fetch('/api/docs/upload', { method: 'POST', body: fd, credentials: 'same-origin' })
  } catch {
    throw new Error('网络错误，上传失败')
  }
  if (res.status === 401) {
    window.dispatchEvent(new CustomEvent('nf-unauthorized'))
    throw new Error('登录已过期，请重新登录')
  }
  const ct = res.headers.get('content-type') || ''
  let data = null
  if (ct.includes('application/json')) {
    try { data = await res.json() } catch { data = null }
  }
  if (!res.ok) throw new Error((data && data.error) || `上传失败（${res.status}）`)
  if (!data?.url) throw new Error('上传成功但未返回地址')
  return data.url
}

export default function Docs() {
  const [list, setList] = useState(null)
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState(null) // null | {id?} new or existing
  const toast = useToast()
  const confirm = useConfirm()

  const load = useCallback(() => {
    setLoading(true)
    api.get('/docs').then(d => setList(d?.docs || [])).catch(err => {
      toast(err.message, 'error')
      setList([])
    }).finally(() => setLoading(false))
  }, [toast])

  useEffect(() => { load() }, [load])

  const del = async (doc) => {
    if (!(await confirm({ title: '删除文档', message: `确认删除「${doc.title}」？删除后用户端将不可见。`, confirmText: '删除', danger: true }))) return
    try {
      await api.del(`/docs/${doc.id}`)
      toast('已删除')
      load()
    } catch (err) {
      toast(err.message, 'error')
    }
  }

  const togglePub = async (doc) => {
    try {
      await api.post(`/docs/${doc.id}/published`, { published: !doc.published })
      toast(doc.published ? '已下架' : '已发布')
      load()
    } catch (err) {
      toast(err.message, 'error')
    }
  }

  const move = async (doc, direction) => {
    try {
      const d = await api.post(`/docs/${doc.id}/move`, { direction })
      setList(d?.docs || [])
    } catch (err) {
      toast(err.message, 'error')
    }
  }

  if (loading) return <Layout><Loading /></Layout>

  if (editing) {
    return (
      <Layout>
        <DocEditor
          doc={editing.id ? list?.find(d => d.id === editing.id) || editing : null}
          onCancel={() => setEditing(null)}
          onSaved={() => { setEditing(null); load() }}
        />
      </Layout>
    )
  }

  return (
    <Layout>
      <div className="h-full flex flex-col">
        <PageHeader title="使用文档" count={list?.length || 0} unit="篇" />
        <Panel fill>
          <PanelToolbar>
            <ToolbarActions>
              <ToolbarButton onClick={() => setEditing({})}>＋ 新建文档</ToolbarButton>
            </ToolbarActions>
          </PanelToolbar>
          <TableScroll>
            {!list || list.length === 0 ? (
              <Empty title="暂无文档" desc="点击右上角「新建文档」写第一篇使用教程，可插入图片。" />
            ) : (
              <table className="tbl">
                <thead>
                  <tr>
                    <th style={{ width: 56 }}>顺序</th>
                    <th>标题</th>
                    <th>状态</th>
                    <th>更新时间</th>
                    <th className="text-right">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {list.map((d, idx) => (
                    <tr key={d.id}>
                      <td className="text-ink-mut font-mono text-xs">{idx + 1}</td>
                      <td className="font-semibold">{d.title}</td>
                      <td>
                        {d.published
                          ? <Badge color="green">已发布</Badge>
                          : <Badge color="gray">草稿</Badge>}
                      </td>
                      <td className="text-xs text-ink-mut">{fmtDate(d.updated_at)}</td>
                      <td className="text-right">
                        <div className="inline-flex items-center gap-2 flex-wrap justify-end">
                          <button type="button" disabled={idx === 0} onClick={() => move(d, 'up')}
                            className="text-xs font-semibold text-ink-soft hover:text-ink disabled:opacity-30">上移</button>
                          <button type="button" disabled={idx === list.length - 1} onClick={() => move(d, 'down')}
                            className="text-xs font-semibold text-ink-soft hover:text-ink disabled:opacity-30">下移</button>
                          <button type="button" onClick={() => togglePub(d)}
                            className="text-xs font-semibold text-blue-600 hover:underline">
                            {d.published ? '下架' : '发布'}
                          </button>
                          <button type="button" onClick={() => setEditing({ id: d.id })}
                            className="text-xs font-semibold text-blue-600 hover:underline">编辑</button>
                          <button type="button" onClick={() => del(d)}
                            className="text-xs font-semibold text-red-600 hover:underline">删除</button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </TableScroll>
        </Panel>
      </div>
    </Layout>
  )
}

function DocEditor({ doc, onCancel, onSaved }) {
  const [title, setTitle] = useState(doc?.title || '')
  const [content, setContent] = useState(doc?.content || '')
  const [published, setPublished] = useState(!!doc?.published)
  const [tab, setTab] = useState('edit') // edit | preview
  const [saving, setSaving] = useState(false)
  const [uploading, setUploading] = useState(false)
  const taRef = useRef(null)
  const fileRef = useRef(null)
  const toast = useToast()

  const insertAtCursor = (snippet) => {
    const el = taRef.current
    if (!el) {
      setContent(c => c + snippet)
      return
    }
    const start = el.selectionStart ?? content.length
    const end = el.selectionEnd ?? content.length
    const next = content.slice(0, start) + snippet + content.slice(end)
    setContent(next)
    requestAnimationFrame(() => {
      el.focus()
      const pos = start + snippet.length
      el.setSelectionRange(pos, pos)
    })
  }

  const doUpload = async (file) => {
    if (!file) return
    if (!file.type?.startsWith('image/')) {
      toast('请选择图片文件', 'error')
      return
    }
    setUploading(true)
    try {
      const url = await uploadDocImage(file)
      const alt = (file.name || '图片').replace(/\.[^.]+$/, '')
      insertAtCursor(`\n![${alt}](${url})\n`)
      toast('图片已插入')
    } catch (err) {
      toast(err.message, 'error')
    } finally {
      setUploading(false)
      if (fileRef.current) fileRef.current.value = ''
    }
  }

  const onPaste = async (e) => {
    const items = e.clipboardData?.items
    if (!items) return
    for (const it of items) {
      if (it.type && it.type.startsWith('image/')) {
        e.preventDefault()
        const file = it.getAsFile()
        if (file) await doUpload(file)
        return
      }
    }
  }

  const save = async () => {
    if (!title.trim()) { toast('标题不能为空', 'error'); return }
    setSaving(true)
    try {
      const body = { title: title.trim(), content, published }
      if (doc?.id) {
        await api.put(`/docs/${doc.id}`, body)
        toast('已保存')
      } else {
        await api.post('/docs', body)
        toast(published ? '已创建并发布' : '已创建草稿')
      }
      onSaved()
    } catch (err) {
      toast(err.message, 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="h-full flex flex-col min-h-0">
      <PageHeader
        title={doc?.id ? '编辑文档' : '新建文档'}
        actions={
          <div className="flex items-center gap-2 flex-wrap">
            <button type="button" onClick={onCancel} className="btn-secondary">返回列表</button>
            <button type="button" disabled={saving} onClick={save} className="btn-primary">
              {saving ? '保存中…' : '保存'}
            </button>
          </div>
        }
      />

      <div className="card flex-1 min-h-0 flex flex-col overflow-hidden">
        <div className="px-5 py-4 border-b border-line-soft flex flex-col gap-3">
          <div className="flex items-center gap-3 flex-wrap">
            <input
              className="input-field flex-1 min-w-[200px]"
              value={title}
              onChange={e => setTitle(e.target.value)}
              placeholder="文档标题，例如：快速入门"
              autoFocus
            />
            <label className="inline-flex items-center gap-2 text-sm font-semibold text-ink-soft cursor-pointer whitespace-nowrap">
              <input type="checkbox" className="accent-blue-600" checked={published} onChange={e => setPublished(e.target.checked)} />
              发布给用户
            </label>
          </div>
          <div className="flex items-center gap-2 flex-wrap">
            <div className="detail-tabs !mb-0 !p-1">
              <button type="button" className={`detail-tab ${tab === 'edit' ? 'is-active' : ''}`} onClick={() => setTab('edit')}>编辑</button>
              <button type="button" className={`detail-tab ${tab === 'preview' ? 'is-active' : ''}`} onClick={() => setTab('preview')}>预览</button>
            </div>
            <div className="ml-auto flex items-center gap-2">
              <input ref={fileRef} type="file" accept="image/jpeg,image/png,image/gif,image/webp" className="hidden"
                onChange={e => doUpload(e.target.files?.[0])} />
              <button type="button" disabled={uploading || tab !== 'edit'} onClick={() => fileRef.current?.click()}
                className="btn-secondary !h-[34px] text-[13px]">
                {uploading ? '上传中…' : '插入图片'}
              </button>
              <span className="text-[12px] text-ink-mut hidden sm:inline">支持粘贴截图 · 单张 ≤ 5MB</span>
            </div>
          </div>
        </div>

        <div className="flex-1 min-h-0 overflow-auto">
          {tab === 'edit' ? (
            <textarea
              ref={taRef}
              className="w-full h-full min-h-[420px] px-5 py-4 text-[14px] leading-relaxed font-mono bg-transparent text-ink outline-none resize-none border-0"
              value={content}
              onChange={e => setContent(e.target.value)}
              onPaste={onPaste}
              placeholder={'用 Markdown 编写教程…\n\n# 标题\n\n正文段落\n\n- 列表项\n\n![说明](图片地址)\n'}
              spellCheck={false}
            />
          ) : (
            <div className="px-6 py-5">
              <h2 className="text-[20px] font-bold text-ink mb-4">{title.trim() || '未命名文档'}</h2>
              <Markdown source={content} />
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
