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
            <div className="flex gap-2">
              <Link to={`/proxy-services/new`} className="btn-secondary text-sm" state={{ from: id }}>再发布</Link>
              <button type="button" className="btn-primary text-sm" onClick={syncRepo}>同步到落地仓库</button>
              <button type="button" className="btn-secondary text-sm text-rose-600" onClick={remove}>删除</button>
            </div>
          }
        />

        <div className="grid lg:grid-cols-3 gap-4 mb-4">
          <Panel>
            <div className="text-[12px] text-ink-mut">协议</div>
            <div className="font-semibold mt-0.5">{svc.protocol}</div>
          </Panel>
          <Panel>
            <div className="text-[12px] text-ink-mut">核心</div>
            <div className="font-mono font-semibold mt-0.5">{svc.core}</div>
          </Panel>
          <Panel>
            <div className="text-[12px] text-ink-mut">订阅可见</div>
            <div className="font-semibold mt-0.5">{svc.sub_visible ? '是' : '否'}</div>
          </Panel>
        </div>

        <Panel fill>
          <div className="flex items-center justify-between mb-3">
            <h3 className="text-sm font-bold">覆盖实例（{instances.length}）</h3>
            <Link to="/proxy-services/new" className="text-sm text-emerald-600 font-semibold">发布到更多节点…</Link>
          </div>
          <p className="text-[12.5px] text-ink-mut mb-3">
            一期为 dry-run：面板生成可导入的分享链接（mieru 为官方 <span className="font-mono">mierus://</span> 格式），
            但 agent 尚未在节点上真正拉起 xray / sing-box / mita 进程。链接可导入客户端，需节点侧已安装并手动配置对应核心后才能通。
          </p>
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
