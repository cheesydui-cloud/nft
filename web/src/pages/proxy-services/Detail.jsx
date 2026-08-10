import { useState, useEffect, useMemo } from 'react'
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

const PROTO_LABEL = {
  vless: 'VLESS',
  shadowsocks: 'Shadowsocks',
  ss: 'Shadowsocks',
  mieru: 'mieru',
}

const DEPLOY_LABEL = {
  ready: { label: '就绪', color: 'green' },
  error: { label: '异常', color: 'red' },
  pending: { label: '待发布', color: 'gray' },
  offline: { label: '离线', color: 'gray' },
}

/** Keys shown in 配置 block, ordered; secrets partially masked. */
const CONFIG_SECTIONS = {
  vless: {
    protocol: [
      'uuid',
      'flow',
      'encryption',
      'decryption',
      'listen_port',
      'share_host',
    ],
    transport: ['network', 'path', 'host', 'xhttp_mode'],
    reality: [
      'server_name',
      'server_port',
      'fingerprint',
      'public_key',
      'private_key',
      'short_id',
      'allow_empty_short_id',
      'spider_x',
      'max_time_difference',
      'security',
    ],
  },
  shadowsocks: {
    protocol: ['method', 'password', 'listen_port', 'share_host'],
  },
  mieru: {
    protocol: [
      'username',
      'password',
      'listen_port',
      'share_host',
      'transports',
      'traffic_pattern',
      'user_hint_is_mandatory',
    ],
  },
}

const FIELD_LABEL = {
  uuid: 'uuid',
  flow: 'flow',
  encryption: 'encryption（客户端）',
  decryption: 'decryption（服务端）',
  listen_port: '默认端口',
  share_host: '分享地址覆盖',
  network: '传输层',
  path: 'path',
  host: 'host',
  xhttp_mode: 'xhttp mode',
  server_name: 'server-name（SNI/dest）',
  server_port: 'server-port（回源）',
  fingerprint: '指纹',
  public_key: 'public_key',
  private_key: 'private_key',
  short_id: 'short_id',
  allow_empty_short_id: '允许空 shortId',
  spider_x: 'spiderX',
  max_time_difference: 'max_time_difference',
  security: 'security',
  method: 'method',
  password: 'password',
  username: 'username',
  transports: 'transports',
  traffic_pattern: 'traffic-pattern',
  user_hint_is_mandatory: 'user_hint_is_mandatory',
}

const SECRET_KEYS = new Set(['private_key', 'password', 'decryption', 'encryption', 'uuid'])

function parseConfig(raw) {
  if (!raw) return {}
  if (typeof raw === 'object' && !Array.isArray(raw)) return raw
  try {
    return JSON.parse(typeof raw === 'string' ? raw : new TextDecoder().decode(raw)) || {}
  } catch {
    return {}
  }
}

function formatVal(key, val) {
  if (val === null || val === undefined || val === '') return null
  if (typeof val === 'boolean') return val ? 'true' : 'false'
  if (Array.isArray(val)) return val.join(', ')
  if (typeof val === 'object') return JSON.stringify(val)
  const s = String(val)
  if (SECRET_KEYS.has(key) && s.length > 20) {
    return `${s.slice(0, 10)}…${s.slice(-6)}`
  }
  return s
}

function formatTime(ts) {
  if (!ts) return '—'
  const d = new Date(Number(ts) * (Number(ts) < 1e12 ? 1000 : 1))
  if (Number.isNaN(d.getTime())) return '—'
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

function ConfigRows({ cfg, keys }) {
  const rows = keys
    .map((k) => {
      const v = formatVal(k, cfg[k])
      if (v == null) return null
      return { k, label: FIELD_LABEL[k] || k, v, full: cfg[k] }
    })
    .filter(Boolean)
  if (rows.length === 0) {
    return <div className="text-[13px] text-ink-mut py-1">无参数</div>
  }
  return (
    <dl className="grid grid-cols-[minmax(120px,180px)_1fr] gap-x-4 gap-y-2 text-[13px]">
      {rows.map(({ k, label, v, full }) => (
        <div key={k} className="contents">
          <dt className="text-ink-mut font-medium">{label}</dt>
          <dd className="font-mono text-[12.5px] break-all min-w-0">
            {SECRET_KEYS.has(k) && full != null && String(full).length > 0 ? (
              <span className="font-mono text-[12.5px] break-all">
                <CopyText text={String(full)}>{v}</CopyText>
              </span>
            ) : (
              <span title={String(full ?? '')}>{v}</span>
            )}
          </dd>
        </div>
      ))}
    </dl>
  )
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
      .then((d) => setSvc(d?.service || null))
      .catch((err) => {
        toast(err.message, 'error')
        setSvc(null)
      })
  }
  useEffect(load, [id])

  const cfg = useMemo(() => parseConfig(svc?.config_json), [svc])
  const proto = String(svc?.protocol || '').toLowerCase()
  const sections = CONFIG_SECTIONS[proto] || CONFIG_SECTIONS.vless

  const syncRepo = async () => {
    try {
      const d = await api.post(`/proxy-services/${id}/sync-repo`, {})
      toast(`已同步 ${d.synced || 0} 条到落地仓库`)
      load()
    } catch (err) {
      toast(err.message, 'error')
    }
  }

  const republish = async () => {
    const nodeIds = [...new Set((svc?.instances || []).map((i) => i.node_id).filter(Boolean))]
    if (nodeIds.length === 0) {
      toast('尚无部署节点', 'error')
      return
    }
    try {
      const r = await api.post(`/proxy-services/${id}/publish`, { node_ids: nodeIds, force_core: true })
      const results = r?.results || []
      const ok = results.filter((x) => x.ok).length
      toast(
        ok === results.length ? `重新发布完成 ${ok}/${results.length}` : `重新发布 ${ok}/${results.length} 成功`,
        ok === results.length ? 'success' : 'error',
      )
      load()
      setLatency(null)
    } catch (err) {
      toast(err.message, 'error')
    }
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
    } catch (err) {
      toast(err.message, 'error')
    }
  }

  if (svc === null) return <Layout><Loading /></Layout>
  if (!svc) return <Layout><Empty title="服务不存在" /></Layout>

  const st = STATUS_MAP[svc.status] || STATUS_MAP.draft
  const instances = svc.instances || []
  const defaultPort = cfg.listen_port || (instances[0] && instances[0].listen_port) || '—'

  return (
    <Layout>
      <div className="h-full flex flex-col overflow-auto">
        <PageHeader
          title={svc.name}
          badge={<Badge color={st.color}>{st.label}</Badge>}
          actions={
            <div className="flex gap-2 flex-wrap">
              <Link to="/proxy-services" className="btn-secondary text-sm">返回列表</Link>
              <Link to={`/proxy-services/${id}/edit`} className="btn-primary text-sm">编辑协议配置</Link>
              <button type="button" className="btn-secondary text-sm" disabled={!(instances.length > 0)} onClick={republish}>
                重新发布
              </button>
              <button type="button" className="btn-secondary text-sm" disabled={probing || !(instances.length > 0)} onClick={probeLatency}>
                {probing ? '测试中…' : '测试'}
              </button>
              <button type="button" className="btn-secondary text-sm" onClick={syncRepo}>同步到落地仓库</button>
            </div>
          }
        />

        {latency?.summary && (
          <div
            className={`mb-4 px-4 py-2.5 rounded-xl border text-[13px] ${
              latency.ok_count > 0 && latency.fail_count === 0
                ? 'border-emerald-300 bg-emerald-50 text-emerald-800 dark:bg-emerald-900/20 dark:text-emerald-200'
                : latency.ok_count > 0
                  ? 'border-amber-300 bg-amber-50 text-amber-900 dark:bg-amber-900/20'
                  : 'border-rose-300 bg-rose-50 text-rose-800 dark:bg-rose-900/20'
            }`}
          >
            测试：{latency.summary}
          </div>
        )}

        {/* 概览 — 对齐 Weir 详情顶栏 */}
        <Panel className="mb-4">
          <div className="px-5 py-4">
            <div className="flex items-center gap-2 mb-3">
              <h2 className="text-base font-bold">{svc.name}</h2>
              <Badge color={st.color}>{st.label}</Badge>
            </div>
            <dl className="grid sm:grid-cols-2 lg:grid-cols-3 gap-x-8 gap-y-2.5 text-[13px]">
              <div className="flex gap-3">
                <dt className="text-ink-mut w-24 shrink-0">协议</dt>
                <dd className="font-semibold">{PROTO_LABEL[proto] || svc.protocol}</dd>
              </div>
              <div className="flex gap-3">
                <dt className="text-ink-mut w-24 shrink-0">核心</dt>
                <dd className="font-mono font-semibold">{svc.core}</dd>
              </div>
              <div className="flex gap-3">
                <dt className="text-ink-mut w-24 shrink-0">默认端口</dt>
                <dd className="font-mono font-semibold">{defaultPort}</dd>
              </div>
              <div className="flex gap-3">
                <dt className="text-ink-mut w-24 shrink-0">覆盖节点数</dt>
                <dd className="font-semibold">{instances.length}</dd>
              </div>
              <div className="flex gap-3">
                <dt className="text-ink-mut w-24 shrink-0">创建时间</dt>
                <dd className="font-mono text-[12.5px]">{formatTime(svc.created_at)}</dd>
              </div>
              <div className="flex gap-3">
                <dt className="text-ink-mut w-24 shrink-0">订阅可见性</dt>
                <dd className="font-semibold">{svc.sub_visible ? '是' : '否'}</dd>
              </div>
            </dl>
          </div>
        </Panel>

        {/* 配置 */}
        <Panel className="mb-4">
          <div className="px-5 py-4 border-b border-line flex items-center justify-between">
            <div>
              <h3 className="text-sm font-bold">配置</h3>
              <p className="text-[12px] text-ink-mut m-0 mt-0.5">协议参数 · 发布到节点时使用的模板</p>
            </div>
            <Link to={`/proxy-services/${id}/edit`} className="text-sm text-emerald-600 font-semibold hover:underline">
              编辑协议配置
            </Link>
          </div>
          <div className="px-5 py-4 space-y-5">
            <div>
              <div className="text-[12px] font-bold text-ink-mut mb-2">协议参数</div>
              <ConfigRows cfg={cfg} keys={sections.protocol || []} />
            </div>
            {sections.transport && (
              <div>
                <div className="text-[12px] font-bold text-ink-mut mb-2">传输</div>
                <ConfigRows cfg={cfg} keys={sections.transport} />
              </div>
            )}
            {sections.reality && (
              <div>
                <div className="text-[12px] font-bold text-ink-mut mb-2">安全层 · REALITY</div>
                <ConfigRows cfg={cfg} keys={sections.reality} />
              </div>
            )}
            {proto === 'vless' && cfg.server_name && (
              <p className="text-[12px] text-ink-mut m-0">
                REALITY dest：
                <span className="font-mono text-ink">
                  {cfg.server_name}:{cfg.server_port || 443}
                </span>
                {' · '}
                客户端需匹配 public_key / short_id / flow
                {cfg.encryption ? ' / encryption' : ''}
              </p>
            )}
          </div>
        </Panel>

        {/* 节点 — 通过哪个节点生成/部署 */}
        <Panel className="mb-4">
          <div className="px-5 py-4 border-b border-line flex items-center justify-between flex-wrap gap-2">
            <div>
              <h3 className="text-sm font-bold">节点</h3>
              <p className="text-[12px] text-ink-mut m-0 mt-0.5">
                本服务已发布到的线路节点（agent 上拉起 {svc.core || '核心'}）
              </p>
            </div>
            <Link
              to={`/proxy-services/${id}/edit`}
              className="btn-primary text-sm"
              title="打开向导，可改配置或发布到更多节点"
            >
              上架到更多节点
            </Link>
          </div>
          <TableScroll>
            {instances.length === 0 ? (
              <Empty title="尚未部署到节点" desc="请通过发布向导选择节点并发布。" />
            ) : (
              <table className="tbl">
                <thead>
                  <tr>
                    <th>名称</th>
                    <th>节点</th>
                    <th>监听端口</th>
                    <th>分享地址</th>
                    <th>状态</th>
                    <th>延迟</th>
                    <th>URI</th>
                    <th className="text-right">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {instances.map((i) => {
                    const ds = DEPLOY_LABEL[i.deploy_status] || { label: i.deploy_status || '—', color: 'gray' }
                    const latR = (latency?.results || []).find(
                      (x) => x.instance_id === i.id || x.node_id === i.node_id,
                    )
                    const rowName = i.node_name || `节点#${i.node_id}`
                    return (
                      <tr key={i.id}>
                        <td className="font-semibold">
                          {svc.name}
                          <span className="text-ink-mut font-normal text-[12px]"> · {rowName}</span>
                        </td>
                        <td>
                          <Link to={`/nodes/${i.node_id}`} className="text-emerald-600 font-semibold hover:underline">
                            {rowName}
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
                          <Badge color={ds.color}>{ds.label}</Badge>
                          {i.last_error && (
                            <div className="text-[11px] text-amber-600 mt-0.5 max-w-[200px]" title={i.last_error}>
                              {i.last_error}
                            </div>
                          )}
                        </td>
                        <td className="text-[12px] font-mono">
                          {probing ? (
                            <span className="text-ink-mut">…</span>
                          ) : !latR ? (
                            <span className="text-ink-mut">—</span>
                          ) : latR.ok ? (
                            <span className="text-emerald-700 dark:text-emerald-300">{latR.latency_ms} ms</span>
                          ) : (
                            <span className="text-rose-600" title={latR.error}>失败</span>
                          )}
                        </td>
                        <td className="max-w-[240px]">
                          {i.uri ? (
                            <span className="text-[11px] font-mono truncate block max-w-[240px]">
                              <CopyText text={i.uri} />
                            </span>
                          ) : '—'}
                        </td>
                        <td className="text-right whitespace-nowrap text-sm">
                          <button
                            type="button"
                            className="text-emerald-600 font-semibold mr-2"
                            onClick={async () => {
                              try {
                                const r = await api.post(`/proxy-services/${id}/publish`, {
                                  node_ids: [i.node_id],
                                  force_core: true,
                                  ports: [{ node_id: i.node_id, port: i.listen_port }],
                                })
                                const ok = (r?.results || []).some((x) => x.ok)
                                toast(ok ? '已重新发布到该节点' : (r?.results?.[0]?.error || '发布失败'), ok ? 'success' : 'error')
                                load()
                              } catch (err) {
                                toast(err.message, 'error')
                              }
                            }}
                          >
                            重新发布
                          </button>
                          <Link to={`/nodes/${i.node_id}`} className="text-emerald-600 font-semibold mr-2">
                            节点
                          </Link>
                          <span className="text-ink-mut text-[11px]">
                            {i.synced_repo_id > 0 ? (
                              <Link to="/node-repo" className="text-emerald-600">仓库 #{i.synced_repo_id}</Link>
                            ) : (
                              '未同步仓库'
                            )}
                          </span>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            )}
          </TableScroll>
        </Panel>

        {/* 危险区 */}
        <Panel className="mb-6 border-rose-200 dark:border-rose-900/50">
          <div className="px-5 py-4">
            <h3 className="text-sm font-bold text-rose-700 dark:text-rose-300 mb-2">危险区</h3>
            <p className="text-[12.5px] text-ink-mut mb-3">
              删除服务不会自动清理节点上已运行的核心进程；落地仓库条目也不会自动删除。
            </p>
            <button type="button" className="btn-secondary text-sm text-rose-600 border-rose-300" onClick={remove}>
              删除服务
            </button>
          </div>
        </Panel>
      </div>
    </Layout>
  )
}
