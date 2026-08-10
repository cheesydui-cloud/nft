import { useState, useEffect } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '../../lib/api'
import { Layout, useToast } from '../../components/Layout'
import { Loading, Empty, Badge, useConfirm } from '../../components/ui'
import { PageHeader, Panel, PanelToolbar, SearchInput, ToolbarButton, ToolbarActions, TableScroll } from '../../components/page'

const STATUS_MAP = {
  draft: { label: '草稿', color: 'gray' },
  ready: { label: '全部就绪', color: 'green' },
  partial: { label: '部分就绪', color: 'amber' },
  error: { label: '异常', color: 'red' },
}

const PROTO_LABEL = {
  vless: 'VLESS',
  shadowsocks: 'Shadowsocks',
  mieru: 'mieru',
}

export default function ProxyServiceList() {
  const [services, setServices] = useState(null)
  const [search, setSearch] = useState('')
  const [probingId, setProbingId] = useState(null)
  const [latencyMap, setLatencyMap] = useState({}) // id -> { summary, ok_count, fail_count, results }
  const toast = useToast()
  const confirm = useConfirm()
  const navigate = useNavigate()

  const load = () => {
    api.get('/proxy-services')
      .then(d => setServices(d?.services || []))
      .catch(err => { toast(err.message, 'error'); setServices([]) })
  }
  useEffect(load, [])

  const remove = async (svc) => {
    if (!(await confirm({ title: '删除代理服务', message: `确定删除「${svc.name}」？已同步到落地仓库的条目不会自动删除。`, confirmText: '删除', danger: true }))) return
    try {
      await api.del(`/proxy-services/${svc.id}`)
      toast('已删除')
      load()
    } catch (err) { toast(err.message, 'error') }
  }

  const probeLatency = async (svc) => {
    setProbingId(svc.id)
    try {
      const d = await api.post(`/proxy-services/${svc.id}/probe-latency`, {})
      setLatencyMap(prev => ({ ...prev, [svc.id]: d }))
      if (d.ok_count > 0 && d.fail_count === 0) toast(d.summary || '探测完成')
      else if (d.ok_count > 0) toast(d.summary || '部分可达', 'error')
      else toast(d.summary || d.error || '探测失败', 'error')
    } catch (err) {
      setLatencyMap(prev => ({ ...prev, [svc.id]: { summary: err.message, ok_count: 0, fail_count: 1 } }))
      toast(err.message, 'error')
    } finally {
      setProbingId(null)
    }
  }

  if (services === null) return <Layout><Loading /></Layout>

  const q = search.trim().toLowerCase()
  const filtered = !q ? services : services.filter(s =>
    [s.name, s.protocol, s.core, s.status].some(v => String(v || '').toLowerCase().includes(q)))

  return (
    <Layout>
      <div className="h-full flex flex-col">
        <PageHeader title="代理服务" count={services.length} unit="个"
          actions={
            <ToolbarButton onClick={() => navigate('/proxy-services/new')}>发布服务</ToolbarButton>
          }
        />
        <Panel fill>
          <PanelToolbar>
            <SearchInput value={search} onChange={setSearch} placeholder="搜索名称、协议、核心…" />
            <ToolbarActions>
              <ToolbarButton onClick={load}>刷新</ToolbarButton>
            </ToolbarActions>
          </PanelToolbar>
          <TableScroll>
            {services.length === 0 ? (
              <Empty title="暂无代理服务" desc="点击「发布服务」，在节点上发布 VLESS / Shadowsocks / mieru，并可同步到落地仓库。" />
            ) : filtered.length === 0 ? (
              <Empty title="无匹配服务" desc="试试别的关键词。" />
            ) : (
              <table className="tbl">
                <thead>
                  <tr>
                    <th>名称</th>
                    <th>协议</th>
                    <th>核心</th>
                    <th>覆盖节点</th>
                    <th>订阅可见</th>
                    <th>状态</th>
                    <th>延迟</th>
                    <th className="text-right">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map(s => {
                    const st = STATUS_MAP[s.status] || STATUS_MAP.draft
                    const lat = latencyMap[s.id]
                    return (
                      <tr key={s.id}>
                        <td>
                          <Link to={`/proxy-services/${s.id}`} className="font-semibold text-emerald-600 hover:underline">
                            {s.name || '(未命名)'}
                          </Link>
                        </td>
                        <td>{PROTO_LABEL[s.protocol] || s.protocol}</td>
                        <td className="font-mono text-[13px]">{s.core}</td>
                        <td>
                          {s.instance_count || 0} 节点
                          {s.ready_count > 0 && s.ready_count !== s.instance_count && (
                            <span className="text-ink-mut text-xs ml-1">({s.ready_count} 就绪)</span>
                          )}
                        </td>
                        <td>{s.sub_visible ? '是' : '否'}</td>
                        <td><Badge color={st.color}>{st.label}</Badge></td>
                        <td className="text-[12px] max-w-[200px]">
                          {probingId === s.id ? (
                            <span className="text-ink-mut">测试中…</span>
                          ) : lat ? (
                            <span className={lat.ok_count > 0 && lat.fail_count === 0 ? 'text-emerald-700 dark:text-emerald-300' : lat.ok_count > 0 ? 'text-amber-700 dark:text-amber-300' : 'text-rose-600'} title={(lat.results || []).map(r => `${r.node_name || r.node_id}: ${r.ok ? r.latency_ms + 'ms' : r.error}`).join('\n')}>
                              {lat.summary || '—'}
                            </span>
                          ) : (
                            <span className="text-ink-mut">—</span>
                          )}
                        </td>
                        <td className="text-right whitespace-nowrap">
                          <button type="button" disabled={probingId === s.id || !(s.instance_count > 0)}
                            onClick={() => probeLatency(s)}
                            className="text-emerald-600 text-sm font-semibold mr-3 disabled:opacity-40 disabled:cursor-not-allowed">
                            {probingId === s.id ? '测试中…' : '测试'}
                          </button>
                          <Link to={`/proxy-services/${s.id}/edit`} className="text-emerald-600 text-sm font-semibold mr-3">编辑</Link>
                          <Link to={`/proxy-services/${s.id}`} className="text-emerald-600 text-sm font-semibold mr-3">查看</Link>
                          <button type="button" onClick={() => remove(s)} className="text-rose-600 text-sm font-semibold">删除</button>
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
    </Layout>
  )
}
