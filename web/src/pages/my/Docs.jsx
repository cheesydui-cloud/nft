import { useState, useEffect, useMemo } from 'react'
import { api } from '../../lib/api'
import { Layout, useToast } from '../../components/Layout'
import { Loading, Empty, ErrorState } from '../../components/ui'
import { PageHeader } from '../../components/page'
import { Markdown } from '../../lib/markdown'

export default function MyDocs() {
  const [list, setList] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [activeId, setActiveId] = useState(null)
  const toast = useToast()

  const load = () => {
    setLoading(true)
    setError('')
    api.get('/my/docs').then(d => {
      const docs = d?.docs || []
      setList(docs)
      setActiveId(prev => {
        if (prev && docs.some(x => x.id === prev)) return prev
        return docs[0]?.id ?? null
      })
    }).catch(err => {
      setError(err.message || '加载失败')
      setList([])
      toast(err.message, 'error')
    }).finally(() => setLoading(false))
  }

  useEffect(() => { load() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const active = useMemo(() => (list || []).find(d => d.id === activeId) || null, [list, activeId])

  if (loading) return <Layout><Loading /></Layout>
  if (error && (!list || list.length === 0)) {
    return (
      <Layout>
        <PageHeader title="使用文档" />
        <ErrorState title="加载失败" desc={error} onRetry={load} />
      </Layout>
    )
  }

  return (
    <Layout>
      <div className="h-full flex flex-col min-h-0">
        <PageHeader title="使用文档" count={list?.length || 0} unit="篇" />
        {!list || list.length === 0 ? (
          <div className="card">
            <Empty title="暂无文档" desc="管理员尚未发布使用教程，请稍后再来查看。" />
          </div>
        ) : (
          <div className="flex-1 min-h-0 grid grid-cols-1 lg:grid-cols-[240px_1fr] gap-4">
            <aside className="card overflow-hidden flex flex-col min-h-0 lg:max-h-full">
              <div className="px-4 py-3 border-b border-line-soft text-[12.5px] font-semibold text-ink-mut tracking-wide">
                目录
              </div>
              <nav className="flex-1 overflow-y-auto p-2 flex flex-col gap-0.5">
                {list.map(d => (
                  <button
                    key={d.id}
                    type="button"
                    onClick={() => setActiveId(d.id)}
                    className={`text-left px-3 py-2.5 rounded-[10px] text-[13.5px] font-medium transition-colors ${
                      d.id === activeId
                        ? 'bg-blue-500/12 text-blue-700 dark:text-blue-300'
                        : 'text-ink-soft hover:bg-raised hover:text-ink'
                    }`}
                  >
                    <span className="line-clamp-2">{d.title}</span>
                  </button>
                ))}
              </nav>
            </aside>
            <section className="card overflow-auto min-h-0 px-6 py-5">
              {active ? (
                <>
                  <h2 className="m-0 text-[22px] font-bold tracking-tight text-ink mb-5">{active.title}</h2>
                  <Markdown source={active.content} />
                </>
              ) : (
                <Empty title="请选择文档" desc="从左侧目录选择一篇文档查看。" />
              )}
            </section>
          </div>
        )}
      </div>
    </Layout>
  )
}
