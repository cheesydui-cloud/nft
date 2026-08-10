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

/** Display order per protocol. Only keys present in config are rendered. */
const CONFIG_FIELDS = {
  vless: [
    { key: 'uuid', label: 'uuid' },
    { key: 'flow', label: 'flow' },
    { key: 'decryption', label: 'decryption' },
    { key: 'encryption', label: 'encryption' },
    { key: 'network', label: 'network' },
    { key: 'path', label: 'path' },
    { key: 'host', label: 'host' },
    { key: 'xhttp_mode', label: 'xhttp_mode' },
    { key: 'security', label: 'security' },
    { key: 'server_name', label: 'server_name' },
    { key: 'server_port', label: 'server_port' },
    { key: 'fingerprint', label: 'fingerprint' },
    { key: 'public_key', label: 'public_key' },
    { key: 'private_key', label: 'private_key' },
    { key: 'short_id', label: 'short_id' },
    { key: 'allow_empty_short_id', label: 'allow_empty_short_id' },
    { key: 'spider_x', label: 'spider_x' },
    { key: 'max_time_difference', label: 'max_time_difference' },
    { key: 'listen_port', label: 'listen_port' },
    { key: 'share_host', label: 'share_host' },
  ],
  shadowsocks: [
    { key: 'method', label: 'method' },
    { key: 'password', label: 'password' },
    { key: 'listen_port', label: 'listen_port' },
    { key: 'share_host', label: 'share_host' },
  ],
  mieru: [
    { key: 'username', label: 'username' },
    { key: 'password', label: 'password' },
    { key: 'transports', label: 'transports' },
    { key: 'traffic_pattern', label: 'traffic_pattern' },
    { key: 'user_hint_is_mandatory', label: 'user_hint_is_mandatory' },
    { key: 'listen_port', label: 'listen_port' },
    { key: 'share_host', label: 'share_host' },
  ],
}

function parseConfig(raw) {
  if (raw == null || raw === '') return {}
  if (typeof raw === 'object' && !Array.isArray(raw)) return raw
  if (typeof raw === 'string') {
    try {
      const o = JSON.parse(raw)
      return o && typeof o === 'object' ? o : {}
    } catch {
      return {}
    }
  }
  return {}
}

function formatValue(val) {
  if (val === null || val === undefined) return null
  if (typeof val === 'boolean') return val ? 'true' : 'false'
  if (Array.isArray(val)) return val.length ? val.join(', ') : null
  if (typeof val === 'object') return JSON.stringify(val)
  const s = String(val)
  return s === '' ? null : s
}

function formatTime(ts) {
  if (!ts) return '—'
  const n = Number(ts)
  const d = new Date(n < 1e12 ? n * 1000 : n)
  if (Number.isNaN(d.getTime())) return '—'
  const p = (x) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

function MetaRow({ label, children }) {
  return (
    <div className="flex gap-3 text-[13px] py-0.5">
      <div className="text-ink-mut w-[7.5rem] shrink-0">{label}</div>
      <div className="min-w-0 break-all">{children}</div>
    </div>
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
    api
      .get(`/proxy-services/${id}`)
      .then((d) => setSvc(d?.service || null))
      .catch((err) => {
        toast(err.message, 'error')
        setSvc(false)
      })
  }
  useEffect(load, [id])

  const cfg = useMemo(() => parseConfig(svc?.config_json), [svc])
  const proto = String(svc?.protocol || '').toLowerCase()
  const fields = CONFIG_FIELDS[proto === 'ss' ? 'shadowsocks' : proto] || CONFIG_FIELDS.vless
  const configRows = useMemo(() => {
    return fields
      .map(({ key, label }) => {
        const text = formatValue(cfg[key])
        if (text == null) return null
        return { key, label, text, raw: cfg[key] }
      })
      .filter(Boolean)
  }, [cfg, fields])

  const instances = svc?.instances || []
  const defaultPort = cfg.listen_port || instances[0]?.listen_port || '—'

  const syncRepo = async () => {
    try {
      const d = await api.post(`/proxy-services/${id}/sync-repo`, {})
      toast(`已同步 ${d.synced || 0} 条到落地仓库`)
      load()
    } catch (err) {
      toast(err.message, 'error')
    }
  }

  const republishAll = async () => {
    const nodeIds = [...new Set(instances.map((i) => i.node_id).filter(Boolean))]
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

  const republishOne = async (inst) => {
    try {
      const r = await api.post(`/proxy-services/${id}/publish`, {
        node_ids: [inst.node_id],
        force_core: true,
        ports: [{ node_id: inst.node_id, port: inst.listen_port }],
      })
      const row = (r?.results || [])[0]
      toast(row?.ok ? '已重新发布到该节点' : row?.error || '发布失败', row?.ok ? 'success' : 'error')
      load()
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
    if (
      !(await confirm({
        title: '删除代理服务',
        message: `确定删除「${svc?.name}」？节点上已运行的核心不会自动停止，落地仓库条目也不会自动删除。`,
        confirmText: '删除',
        danger: true,
      }))
    ) {
      return
    }
    try {
      await api.del(`/proxy-services/${id}`)
      toast('已删除')
      navigate('/proxy-services')
    } catch (err) {
      toast(err.message, 'error')
    }
  }

  if (svc === null) {
    return (
      <Layout>
        <Loading />
      </Layout>
    )
  }
  if (!svc) {
    return (
      <Layout>
        <Empty title="服务不存在" />
      </Layout>
    )
  }

  const st = STATUS_MAP[svc.status] || STATUS_MAP.draft

  return (
    <Layout>
      <div className="h-full flex flex-col overflow-auto">
        <PageHeader
          title={svc.name}
          badge={<Badge color={st.color}>{st.label}</Badge>}
          actions={
            <div className="flex gap-2 flex-wrap">
              <Link to="/proxy-services" className="btn-secondary text-sm">
                返回列表
              </Link>
              <Link to={`/proxy-services/${id}/edit`} className="btn-primary text-sm">
                编辑协议配置
              </Link>
              <button
                type="button"
                className="btn-secondary text-sm"
                disabled={instances.length === 0}
                onClick={republishAll}
              >
                重新发布
              </button>
              <button
                type="button"
                className="btn-secondary text-sm"
                disabled={probing || instances.length === 0}
                onClick={probeLatency}
              >
                {probing ? '测试中…' : '测试'}
              </button>
              <button type="button" className="btn-secondary text-sm" onClick={syncRepo}>
                同步到落地仓库
              </button>
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

        {/* 概览 */}
        <Panel className="mb-4">
          <div className="px-5 py-4 space-y-1">
            <MetaRow label="协议">{PROTO_LABEL[proto] || svc.protocol}</MetaRow>
            <MetaRow label="核心">
              <span className="font-mono">{svc.core}</span>
            </MetaRow>
            <MetaRow label="默认端口">
              <span className="font-mono">{defaultPort}</span>
            </MetaRow>
            <MetaRow label="覆盖节点数">{instances.length}</MetaRow>
            <MetaRow label="创建时间">
              <span className="font-mono text-[12.5px]">{formatTime(svc.created_at)}</span>
            </MetaRow>
            <MetaRow label="订阅可见性">{svc.sub_visible ? '是' : '否'}</MetaRow>
          </div>
        </Panel>

        {/* 配置 */}
        <Panel className="mb-4">
          <div className="px-5 py-3 border-b border-line flex items-center justify-between">
            <div>
              <div className="text-sm font-bold">配置</div>
              <div className="text-[12px] text-ink-mut">协议参数（发布到节点时使用的模板）</div>
            </div>
            <Link to={`/proxy-services/${id}/edit`} className="text-sm text-emerald-600 font-semibold hover:underline">
              编辑协议配置
            </Link>
          </div>
          <div className="px-5 py-4">
            {configRows.length === 0 ? (
              <div className="text-[13px] text-ink-mut">无配置字段</div>
            ) : (
              <div className="space-y-2">
                {configRows.map(({ key, label, text, raw }) => (
                  <MetaRow key={key} label={label}>
                    <span className="font-mono text-[12.5px]">
                      <CopyText text={String(raw)}>{text}</CopyText>
                    </span>
                  </MetaRow>
                ))}
              </div>
            )}
            {proto === 'vless' && cfg.server_name && (
              <p className="text-[12px] text-ink-mut mt-4 mb-0">
                REALITY dest：
                <span className="font-mono text-ink">
                  {cfg.server_name}:{cfg.server_port || 443}
                </span>
                {cfg.security === 'reality' || !cfg.security ? ' · security=reality' : ''}
              </p>
            )}
          </div>
        </Panel>

        {/* 节点 */}
        <Panel className="mb-4">
          <div className="px-5 py-3 border-b border-line flex items-center justify-between flex-wrap gap-2">
            <div>
              <div className="text-sm font-bold">节点</div>
              <div className="text-[12px] text-ink-mut">
                本服务已发布到的线路节点（agent 上运行 {svc.core || '核心'}）
              </div>
            </div>
            <Link to={`/proxy-services/${id}/edit`} className="btn-primary text-sm">
              编辑 / 发布到更多节点
            </Link>
          </div>
          <TableScroll>
            {instances.length === 0 ? (
              <Empty title="尚未部署到节点" desc="请点「编辑 / 发布到更多节点」，在向导中选择节点并发布。" />
            ) : (
              <table className="tbl">
                <thead>
                  <tr>
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
                    const ds = DEPLOY_LABEL[i.deploy_status] || {
                      label: i.deploy_status || '—',
                      color: 'gray',
                    }
                    const latR = (latency?.results || []).find(
                      (x) => x.instance_id === i.id || x.node_id === i.node_id,
                    )
                    const nodeName = i.node_name || `#${i.node_id}`
                    return (
                      <tr key={i.id}>
                        <td>
                          <Link
                            to={`/nodes/${i.node_id}`}
                            className="font-semibold text-emerald-600 hover:underline"
                          >
                            {nodeName}
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
                          {i.last_error ? (
                            <div className="text-[11px] text-amber-600 mt-0.5 max-w-[220px]" title={i.last_error}>
                              {i.last_error}
                            </div>
                          ) : null}
                        </td>
                        <td className="text-[12px] font-mono">
                          {probing ? (
                            <span className="text-ink-mut">…</span>
                          ) : !latR ? (
                            <span className="text-ink-mut">—</span>
                          ) : latR.ok ? (
                            <span className="text-emerald-700 dark:text-emerald-300">{latR.latency_ms} ms</span>
                          ) : (
                            <span className="text-rose-600" title={latR.error}>
                              失败
                            </span>
                          )}
                        </td>
                        <td className="max-w-[260px]">
                          {i.uri ? (
                            <span className="text-[11px] font-mono truncate block max-w-[260px]">
                              <CopyText text={i.uri} />
                            </span>
                          ) : (
                            '—'
                          )}
                        </td>
                        <td className="text-right whitespace-nowrap text-sm">
                          <button
                            type="button"
                            className="text-emerald-600 font-semibold mr-3"
                            onClick={() => republishOne(i)}
                          >
                            重新发布
                          </button>
                          {i.synced_repo_id > 0 ? (
                            <Link to="/node-repo" className="text-emerald-600 font-semibold">
                              仓库 #{i.synced_repo_id}
                            </Link>
                          ) : (
                            <span className="text-ink-mut text-[12px]">未同步仓库</span>
                          )}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            )}
          </TableScroll>
        </Panel>

        <Panel className="mb-6 border-rose-200 dark:border-rose-900/40">
          <div className="px-5 py-4">
            <div className="text-sm font-bold text-rose-700 dark:text-rose-300 mb-1">危险区</div>
            <p className="text-[12.5px] text-ink-mut mb-3">
              删除仅移除面板记录；不会自动停节点上的 xray/sing-box/mita，也不会删落地仓库条目。
            </p>
            <button type="button" className="btn-secondary text-sm text-rose-600" onClick={remove}>
              删除服务
            </button>
          </div>
        </Panel>
      </div>
    </Layout>
  )
}
