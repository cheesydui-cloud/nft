import { useState, useEffect } from 'react'
import { Link, useParams, useNavigate } from 'react-router-dom'
import { api } from '../../lib/api'
import { Layout, useToast } from '../../components/Layout'
import { Loading, Empty, Badge, useConfirm, CopyText } from '../../components/ui'
import { PageHeader, Panel, TableScroll } from '../../components/page'

const STATUS_MAP = {
  draft: { label: '草稿', color: 'gray' },
  ready: { label: '全部就绪', color: 'green' },
  partial: { label: '部分就绪', color: 'amber' },
  error: { label: '异常', color: 'red' },
}

export default function ProxyServiceDetail() {
  const { id } = useParams()
  const [svc, setSvc] = useState(null)
  const [probing, setProbing] = useState(false)
  const [latency, setLatency] = useState(null)
  const toast = useToast()
  const confirm = useConfirm()
  const navigate = useNavigate()

  const load = () => {
    api.get(`/proxy-services/${id}`)
      .then(d => setSvc(d?.service || null))
      .catch(err => { toast(err.message, 'error'); setSvc(null) })
  }
  useEffect(load, [id])

  const syncRepo = async () => {
    try {
      const d = await api.post(`/proxy-services/${id}/sync-repo`, {})
      toast(`已同步 ${d.synced || 0} 条到落地仓库`)
      load()
    } catch (err) { toast(err.message, 'error') }
  }

  const republish = async () => {
    const nodeIds = [...new Set((svc?.instances || []).map(i => i.node_id).filter(Boolean))]
    if (nodeIds.length === 0) { toast('尚无部署节点', 'error'); return }
    try {
      const r = await api.post(`/proxy-services/${id}/publish`, { node_ids: nodeIds, force_core: true })
      const results = r?.results || []
      const ok = results.filter(x => x.ok).length
      toast(ok === results.length ? `重新发布完成 ${ok}/${results.length}` : `重新发布 ${ok}/${results.length} 成功`, ok === results.length ? 'success' : 'error')
      load()
      setLatency(null)
    } catch (err) { toast(err.message, 'error') }
  }

  const probeLatency = async () => {
    setProbing(true)
    try {
      const d = await api.post(`/proxy-services/${id}/probe-latency`, {})
      setLatency(d)
      if (d.ok_count > 0 && d.fail_count === 0) toast(d.summary || '探测完成')
      else toast(d.summary || '探测完成（有失败）', 'error')
    } catch (err) {
      setLatency({ summary: err.message, ok_count: 0, fail_count: 1, results: [] })
      toast(err.message, 'error')
    } finally {
      setProbing(false)
    }
  }

  const remove = async () => {
    if (!(await confirm({ title: '删除代理服务', message: `确定删除「${svc?.name}」？`, confirmText: '删除', danger: true }))) return
    try {
      await api.del(`/proxy-services/${id}`)
      toast('已删除')
      navigate('/proxy-services')
    } catch (err) { toast(err.message, 'error') }
  }

  if (svc === null) return <Layout><Loading /></Layout>
  if (!svc) return <Layout><Empty title="服务不存在" /></Layout>

  const st = STATUS_MAP[svc.status] || STATUS_MAP.draft
  const instances = svc.instances || []

  return (
    <Layout>
      <div className="h-full flex flex-col">
        <PageHeader
          title={svc.name}
          badge={<Badge color={st.color}>{st.label}</Badge>}
          actions={
            <div className="flex gap-2 flex-wrap">
              <Link to={`/proxy-services/${id}/edit`} className="btn-primary text-sm">编辑</Link>
              <button type="button" className="btn-secondary text-sm" disabled={!(instances.length > 0)} onClick={republish}>重新发布</button>
              <button type="button" className="btn-secondary text-sm" disabled={probing || !(instances.length > 0)} onClick={probeLatency}>
                {probing ? '测试中…' : '测试'}
              </button>
              <Link to={`/proxy-services/new`} className="btn-secondary text-sm" state={{ from: id }}>再发布</Link>
              <button type="button" className="btn-secondary text-sm" onClick={syncRepo}>同步到落地仓库</button>
              <button type="button" className="btn-secondary text-sm text-rose-600" onClick={remove}>删除</button>
            </div>
          }
        />

        {latency?.summary && (
          <div className={`mb-4 px-4 py-2.5 rounded-xl border text-[13px] ${
            latency.ok_count > 0 && latency.fail_count === 0
              ? 'border-emerald-300 bg-emerald-50 text-emerald-800 dark:bg-emerald-900/20 dark:text-emerald-200'
              : latency.ok_count > 0
                ? 'border-amber-300 bg-amber-50 text-amber-900 dark:bg-amber-900/20'
                : 'border-rose-300 bg-rose-50 text-rose-800 dark:bg-rose-900/20'
          }`}>
            测试：{latency.summary}
          </div>
        )}

        <div className="grid lg:grid-cols-3 gap-4 mb-4">
          <Panel>
            <div className="px-5 py-4">
              <div className="text-[12px] text-ink-mut">协议</div>
              <div className="font-semibold mt-0.5">{svc.protocol}</div>
            </div>
          </Panel>
          <Panel>
            <div className="px-5 py-4">
              <div className="text-[12px] text-ink-mut">核心</div>
              <div className="font-mono font-semibold mt-0.5">{svc.core}</div>
            </div>
          </Panel>
          <Panel>
            <div className="px-5 py-4">
              <div className="text-[12px] text-ink-mut">订阅可见</div>
              <div className="font-semibold mt-0.5">{svc.sub_visible ? '是' : '否'}</div>
            </div>
          </Panel>
        </div>

        <Panel fill>
          <div className="px-5 pt-4 pb-2">
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-sm font-bold">覆盖实例（{instances.length}）</h3>
              <Link to="/proxy-services/new" className="text-sm text-emerald-600 font-semibold">发布到更多节点…</Link>
            </div>
            <p className="text-[12.5px] text-ink-mut mb-3">
              发布时 agent 会在节点真实拉起核心进程：
              <strong> VLESS → xray</strong>（独立配置 <span className="font-mono">/var/lib/nft/cores/xray/</span>）、
              <strong> Shadowsocks → sing-box</strong>（<span className="font-mono">/var/lib/nft/cores/sing-box/</span>）、
              <strong> mieru → mita</strong>（<span className="font-mono">mita apply/start</span>）。
              节点需已安装对应二进制；error 常见原因：未装核心、REALITY 缺 server-name、端口占用、防火墙未放行。
            </p>
          </div>
          <TableScroll>
            {instances.length === 0 ? (
              <Empty title="尚未部署到节点" desc="请通过发布向导选择节点。" />
            ) : (
              <table className="tbl">
                <thead>
                  <tr>
                    <th>节点</th>
                    <th>端口</th>
                    <th>分享地址</th>
                    <th>状态</th>
                    <th>延迟</th>
                    <th>落地仓库</th>
                    <th>URI</th>
                  </tr>
                </thead>
                <tbody>
                  {instances.map(i => (
                    <tr key={i.id}>
                      <td>
                        <Link to={`/nodes/${i.node_id}`} className="font-semibold text-emerald-600 hover:underline">
                          {i.node_name || `#${i.node_id}`}
                        </Link>
                        {i.node_online ? (
                          <span className="text-[11px] text-emerald-600 ml-1.5">在线</span>
                        ) : (
                          <span className="text-[11px] text-ink-mut ml-1.5">离线</span>
                        )}
                      </td>
                      <td className="font-mono">{i.listen_port}</td>
                      <td className="font-mono text-[12px]">{i.share_host || '—'}</td>
                      <td>
                        <Badge color={i.deploy_status === 'ready' ? 'green' : i.deploy_status === 'error' ? 'red' : 'gray'}>
                          {i.deploy_status}
                        </Badge>
                        {i.last_error && <div className="text-[11px] text-amber-600 mt-0.5 max-w-[200px]">{i.last_error}</div>}
                      </td>
                      <td className="text-[12px] font-mono">
                        {(() => {
                          const r = (latency?.results || []).find(x => x.instance_id === i.id || x.node_id === i.node_id)
                          if (probing) return <span className="text-ink-mut">…</span>
                          if (!r) return <span className="text-ink-mut">—</span>
                          if (r.ok) return <span className="text-emerald-700 dark:text-emerald-300">{r.latency_ms} ms</span>
                          return <span className="text-rose-600" title={r.error}>失败</span>
                        })()}
                      </td>
                      <td>
                        {i.synced_repo_id > 0
                          ? <Link to="/node-repo" className="text-emerald-600 text-sm">#{i.synced_repo_id}</Link>
                          : <span className="text-ink-mut text-sm">未同步</span>}
                      </td>
                      <td className="max-w-[280px]">
                        {i.uri ? <CopyText text={i.uri} className="text-[11px] font-mono truncate block" /> : '—'}
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
