import { useState, useEffect, useMemo } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api } from '../../lib/api'
import { Layout, useToast } from '../../components/Layout'
import { Loading, Empty, Badge, CopyText } from '../../components/ui'
import { PageHeader, Panel } from '../../components/page'

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
  socks5: 'SOCKS5',
  socks: 'SOCKS5',
  anytls: 'AnyTLS',
  naive: 'Naive',
  naiveproxy: 'Naive',
}

const DEPLOY_LABEL = {
  ready: { label: '就绪', color: 'green' },
  error: { label: '异常', color: 'red' },
  pending: { label: '待发布', color: 'gray' },
  offline: { label: '离线', color: 'gray' },
}

/** Grouped fields for config display. Only keys present in config are shown. */
const CONFIG_GROUPS = {
  vless: [
    {
      title: '协议参数',
      keys: [
        ['uuid', 'uuid'],
        ['security', 'security'],
        ['flow', 'flow'],
        ['decryption', 'decryption'],
        ['encryption', 'encryption'],
        ['listen_port', 'listen_port'],
        ['share_host', 'share_host'],
      ],
    },
    {
      title: '传输',
      keys: [
        ['network', 'network'],
        ['path', 'path'],
        ['host', 'host'],
        ['xhttp_mode', 'xhttp_mode'],
        ['service_name', 'service_name'],
      ],
    },
    {
      title: '安全层 · REALITY',
      keys: [
        ['server_name', 'server_name'],
        ['server_port', 'server_port'],
        ['fingerprint', 'fingerprint'],
        ['public_key', 'public_key'],
        ['private_key', 'private_key'],
        ['short_id', 'short_id'],
        ['allow_empty_short_id', 'allow_empty_short_id'],
        ['spider_x', 'spider_x'],
        ['max_time_difference', 'max_time_difference'],
      ],
      // only show when security is reality (or empty legacy)
      when: (cfg) => !cfg.security || cfg.security === 'reality',
    },
    {
      title: '安全层 · TLS',
      keys: [
        ['server_name', 'server_name'],
        ['fingerprint', 'fingerprint'],
        ['alpn', 'alpn'],
        ['allow_insecure', 'allow_insecure'],
        ['cert_configured', '证书'],
        ['key_configured', '私钥'],
        ['cert_info', '证书信息'],
        ['acme_enabled', 'ACME 自动续期'],
        ['acme_domain', 'ACME 域名'],
        ['acme_issuer', 'ACME 签发方'],
        ['acme_not_after', 'ACME 到期'],
        ['acme_last_renew_at', '上次续期'],
        ['acme_last_error', 'ACME 错误'],
      ],
      when: (cfg) => cfg.security === 'tls',
    },
    {
      title: '高级',
      keys: [
        ['sniffing', 'sniffing'],
        ['tcp_fast_open', 'tcp_fast_open'],
      ],
    },
  ],
  shadowsocks: [
    {
      title: '协议参数',
      keys: [
        ['method', 'method'],
        ['password', 'password'],
        ['password_configured', '密码'],
        ['listen_port', 'listen_port'],
        ['share_host', 'share_host'],
        ['listen', 'listen'],
      ],
    },
    {
      title: '高级（sing-box）',
      keys: [
        ['ntp', 'NTP'],
        ['sniffing', 'sniff'],
        ['tcp_fast_open', 'tcp_fast_open'],
        ['multiplex', 'multiplex'],
      ],
    },
  ],
  mieru: [
    {
      title: '协议参数',
      keys: [
        ['username', 'username'],
        ['password', 'password'],
        ['transports', 'transports'],
        ['traffic_pattern', 'traffic_pattern'],
        ['user_hint_is_mandatory', 'user_hint_is_mandatory'],
        ['listen_port', 'listen_port'],
        ['share_host', 'share_host'],
      ],
    },
  ],
  socks5: [
    {
      title: '协议参数',
      keys: [
        ['auth_mode', 'auth_mode'],
        ['username', 'username'],
        ['password', 'password'],
        ['password_configured', '密码'],
        ['listen_port', 'listen_port'],
        ['share_host', 'share_host'],
        ['listen', 'listen'],
        ['udp', 'UDP'],
      ],
    },
    {
      title: '高级',
      keys: [
        ['ntp', 'NTP'],
        ['sniffing', 'sniff'],
        ['tcp_fast_open', 'tcp_fast_open'],
      ],
    },
  ],
  anytls: [
    {
      title: '协议参数',
      keys: [
        ['username', 'username'],
        ['password', 'password'],
        ['password_configured', '密码'],
        ['listen_port', 'listen_port'],
        ['share_host', 'share_host'],
        ['listen', 'listen'],
      ],
    },
    {
      title: 'TLS',
      keys: [
        ['server_name', 'server_name'],
        ['fingerprint', 'fingerprint'],
        ['alpn', 'alpn'],
        ['allow_insecure', 'allow_insecure'],
        ['cert_configured', '证书'],
        ['key_configured', '私钥'],
      ],
    },
    {
      title: '高级',
      keys: [
        ['ntp', 'NTP'],
        ['sniffing', 'sniff'],
        ['tcp_fast_open', 'tcp_fast_open'],
      ],
    },
  ],
  naive: [
    {
      title: '协议参数',
      keys: [
        ['username', 'username'],
        ['password', 'password'],
        ['password_configured', '密码'],
        ['network', 'network'],
        ['listen_port', 'listen_port'],
        ['share_host', 'share_host'],
        ['listen', 'listen'],
      ],
    },
    {
      title: 'TLS',
      keys: [
        ['server_name', 'server_name'],
        ['alpn', 'alpn'],
        ['allow_insecure', 'allow_insecure'],
        ['cert_configured', '证书'],
        ['key_configured', '私钥'],
        ['quic_congestion_control', 'quic_cc'],
      ],
    },
    {
      title: '高级',
      keys: [
        ['ntp', 'NTP'],
        ['sniffing', 'sniff'],
        ['tcp_fast_open', 'tcp_fast_open'],
      ],
    },
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

function formatValue(val, key) {
  if (val === null || val === undefined) return null
  if (key === 'cert_configured' || key === 'key_configured' || key === 'private_key_configured' || key === 'decryption_configured') {
    return val ? '已配置' : null
  }
  if (key === 'cert_info' && typeof val === 'object') {
    const parts = []
    if (val.cn) parts.push(`CN ${val.cn}`)
    if (val.not_after) {
      let exp = `至 ${val.not_after}`
      if (val.expired) exp += ' · 已过期'
      else if (val.days_left != null) exp += ` · 剩 ${val.days_left} 天`
      if (val.expiring && !val.expired) exp += ' · 即将到期'
      parts.push(exp)
    }
    if (val.key_match === false) parts.push('私钥不匹配')
    if (val.san_match === false) parts.push('SNI 不在 SAN')
    if (val.fingerprint) parts.push(`SHA256 ${String(val.fingerprint).slice(0, 16)}…`)
    return parts.length ? parts.join(' · ') : '已配置'
  }
  if (typeof val === 'boolean') return val ? 'true' : 'false'
  if (Array.isArray(val)) return val.length ? val.join(', ') : null
  if (typeof val === 'object') return JSON.stringify(val)
  const s = String(val)
  if (s === '') return null
  // Redact bulky / sensitive PEM material in detail view.
  if (key === 'cert_pem' || key === 'key_pem') {
    return '已配置（服务端已脱敏）'
  }
  if (key === 'private_key' && s.length > 12) {
    return s.slice(0, 6) + '…' + s.slice(-4)
  }
  return s
}

function formatTime(ts) {
  if (!ts) return '—'
  const n = Number(ts)
  const d = new Date(n < 1e12 ? n * 1000 : n)
  if (Number.isNaN(d.getTime())) return '—'
  const p = (x) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

function KvRow({ label, children }) {
  return (
    <div className="grid grid-cols-[9rem_1fr] sm:grid-cols-[11rem_1fr] gap-x-4 gap-y-1 py-1.5 text-[13px] items-start">
      <div className="text-ink-mut shrink-0 pt-0.5">{label}</div>
      <div className="min-w-0 break-all">{children}</div>
    </div>
  )
}

export default function ProxyServiceDetail() {
  const { id } = useParams()
  const [svc, setSvc] = useState(null)
  const [probing, setProbing] = useState(false)
  const [latency, setLatency] = useState(null)
  const [acmeBusy, setAcmeBusy] = useState(false)
  const toast = useToast()

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
  const protoKey = ({
    ss: 'shadowsocks',
    socks: 'socks5',
    naiveproxy: 'naive',
  })[proto] || proto
  const groups = CONFIG_GROUPS[protoKey] || CONFIG_GROUPS.vless

  const groupedRows = useMemo(() => {
    return groups
      .map((g) => {
        if (typeof g.when === 'function' && !g.when(cfg)) return null
        const rows = g.keys
          .map(([key, label]) => {
            const text = formatValue(cfg[key], key)
            if (text == null) return null
            // Don't put full PEM on clipboard for CopyText
            const raw = (key === 'cert_pem' || key === 'key_pem') ? text : cfg[key]
            return { key, label, text, raw }
          })
          .filter(Boolean)
        return rows.length ? { title: g.title, rows } : null
      })
      .filter(Boolean)
  }, [cfg, groups])

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

  const issueACME = async () => {
    const domain = (cfg.server_name || cfg.acme_domain || '').trim()
    if (!domain) {
      toast('请先在编辑页填写 server_name（TLS 域名）', 'error')
      return
    }
    setAcmeBusy(true)
    try {
      const d = await api.post(`/proxy-services/${id}/acme`, {
        domain,
        staging: false,
        republish: true,
      })
      toast(
        `证书已签发至 ${d.not_after || ''}${d.publish_note ? ' · ' + d.publish_note : ''}`,
      )
      load()
    } catch (err) {
      toast(err.message || 'ACME 失败', 'error')
    } finally {
      setAcmeBusy(false)
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

  /** Real public RTT: panel dials share_host:port (default). mode=local = agent loopback. */
  const probeLatency = async ({ silent = false, mode = 'public' } = {}) => {
    setProbing(true)
    try {
      const d = await api.post(`/proxy-services/${id}/probe-latency?mode=${mode}`, { mode })
      setLatency(d)
      if (!silent) {
        if (d.ok_count > 0 && d.fail_count === 0) toast(d.summary || '延迟测试完成')
        else toast(d.summary || '延迟测试完成（有失败）', 'error')
      }
    } catch (err) {
      setLatency({ summary: err.message, ok_count: 0, fail_count: 1, results: [] })
      if (!silent) toast(err.message, 'error')
    } finally {
      setProbing(false)
    }
  }

  // Auto-measure public latency once instances are loaded.
  useEffect(() => {
    if (!svc || !instances.length) return
    probeLatency({ silent: true, mode: 'public' })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, svc?.id, instances.length])

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
      {/* Document flow only — Layout main scrolls. Do NOT use h-full / nested overflow. */}
      <div className="space-y-4 pb-8">
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
                onClick={() => probeLatency({ mode: 'public' })}
                title="面板直连分享地址:端口，测公网 TCP 延迟"
              >
                {probing ? '测延迟…' : '测延迟'}
              </button>
              <button
                type="button"
                className="btn-secondary text-sm"
                disabled={probing || instances.length === 0}
                onClick={() => probeLatency({ mode: 'local' })}
                title="节点 agent 本机探测监听端口（核心是否存活）"
              >
                本机探测
              </button>
              <button type="button" className="btn-secondary text-sm" onClick={syncRepo}>
                同步到落地仓库
              </button>
            </div>
          }
        />

        {latency?.summary && (
          <div
            className={`px-4 py-2.5 rounded-xl border text-[13px] ${
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

        {/* 概览 — short grid, never clipped */}
        <Panel>
          <div className="px-5 py-4">
            <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-x-8 gap-y-3 text-[13px]">
              <div className="flex gap-3 min-w-0">
                <span className="text-ink-mut w-24 shrink-0">协议</span>
                <span className="font-semibold">{PROTO_LABEL[proto] || svc.protocol}</span>
              </div>
              <div className="flex gap-3 min-w-0">
                <span className="text-ink-mut w-24 shrink-0">核心</span>
                <span className="font-mono font-semibold">{svc.core}</span>
              </div>
              <div className="flex gap-3 min-w-0">
                <span className="text-ink-mut w-24 shrink-0">默认端口</span>
                <span className="font-mono font-semibold">{defaultPort}</span>
              </div>
              <div className="flex gap-3 min-w-0">
                <span className="text-ink-mut w-24 shrink-0">覆盖节点数</span>
                <span className="font-semibold">{instances.length}</span>
              </div>
              <div className="flex gap-3 min-w-0">
                <span className="text-ink-mut w-24 shrink-0">创建时间</span>
                <span className="font-mono text-[12.5px]">{formatTime(svc.created_at)}</span>
              </div>
              <div className="flex gap-3 min-w-0">
                <span className="text-ink-mut w-24 shrink-0">订阅可见性</span>
                <span className="font-semibold">{svc.sub_visible ? '是' : '否'}</span>
              </div>
            </div>
          </div>
        </Panel>

        {/* 配置 — full key/value, grouped, document height */}
        <Panel>
          <div className="px-5 py-3 border-b border-line flex items-center justify-between gap-3 flex-wrap">
            <div>
              <div className="text-sm font-bold">配置</div>
              <div className="text-[12px] text-ink-mut">协议参数（发布到节点时使用的模板）</div>
            </div>
            <Link to={`/proxy-services/${id}/edit`} className="text-sm text-emerald-600 font-semibold hover:underline">
              编辑协议配置
            </Link>
          </div>
          <div className="px-5 py-4 space-y-5">
            {groupedRows.length === 0 ? (
              <div className="text-[13px] text-ink-mut">无配置字段</div>
            ) : (
              groupedRows.map((g) => (
                <div key={g.title}>
                  <div className="text-[12px] font-bold text-ink-mut mb-1.5">{g.title}</div>
                  <div className="rounded-lg border border-line/70 px-3 sm:px-4 py-1">
                    {g.rows.map(({ key, label, text, raw }) => (
                      <KvRow key={key} label={label}>
                        <span className="font-mono text-[12.5px] leading-relaxed">
                          <CopyText text={String(raw)}>{text}</CopyText>
                        </span>
                      </KvRow>
                    ))}
                  </div>
                </div>
              ))
            )}
            {proto === 'vless' && cfg.server_name && (cfg.security === 'reality' || !cfg.security) && (
              <p className="text-[12px] text-ink-mut m-0">
                REALITY dest：
                <span className="font-mono text-ink">
                  {cfg.server_name}:{cfg.server_port || 443}
                </span>
                {' · security=reality'}
              </p>
            )}
            {proto === 'vless' && cfg.security === 'tls' && cfg.server_name && (
              <div className="space-y-1.5">
                <p className={`text-[12px] m-0 ${
                  cfg.cert_info?.expired ? 'text-rose-600 dark:text-rose-300'
                    : cfg.cert_info?.expiring ? 'text-amber-700 dark:text-amber-300'
                    : 'text-ink-mut'
                }`}>
                  TLS SNI：
                  <span className="font-mono text-ink">{cfg.server_name}</span>
                  {' · security=tls'}
                  {(cfg.cert_configured || cfg.cert_pem) ? ' · 证书已配置' : ' · 证书缺失'}
                  {cfg.acme_enabled ? ' · ACME 自动续期' : ''}
                  {(cfg.cert_info?.not_after || cfg.acme_not_after) && (
                    <> · 到期 {cfg.cert_info?.not_after || cfg.acme_not_after}{cfg.cert_info?.expired ? '（已过期）' : cfg.cert_info?.expiring ? '（即将到期）' : ''}</>
                  )}
                </p>
                {cfg.acme_last_error && (
                  <p className="text-[12px] text-rose-600 m-0">ACME 错误：{cfg.acme_last_error}</p>
                )}
                <button
                  type="button"
                  className="btn-secondary text-xs"
                  disabled={acmeBusy}
                  onClick={issueACME}
                >
                  {acmeBusy ? 'ACME 申请中…' : (cfg.acme_enabled || cfg.cert_configured ? '续期 / 重新申请 ACME' : '申请 Let\'s Encrypt')}
                </button>
              </div>
            )}
            {proto === 'vless' && cfg.security === 'none' && (
              <p className="text-[12px] text-amber-700 dark:text-amber-300 m-0">
                安全层为「无」· 明文传输，勿暴露公网
              </p>
            )}
          </div>
        </Panel>

        {/* 节点 — plain table, horizontal scroll only, no flex height trap */}
        <Panel>
          <div className="px-5 py-3 border-b border-line flex items-center justify-between gap-3 flex-wrap">
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
          {instances.length === 0 ? (
            <div className="px-5 py-8">
              <Empty title="尚未部署到节点" desc="请点「编辑 / 发布到更多节点」，在向导中选择节点并发布。" />
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="tbl w-full min-w-[640px]">
                <thead>
                  <tr>
                    <th>节点</th>
                    <th>监听端口</th>
                    <th>分享地址</th>
                    <th>状态</th>
                    <th>延迟</th>
                    <th>链接</th>
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
                        <td className="whitespace-nowrap">
                          <Link
                            to={`/nodes/${i.node_id}`}
                            className="font-semibold text-emerald-600 hover:underline"
                          >
                            {nodeName}
                          </Link>
                          {i.node_online ? (
                            <span className="text-[11px] text-green-600 ml-1.5">在线</span>
                          ) : (
                            <span className="text-[11px] text-ink-mut ml-1.5">离线</span>
                          )}
                        </td>
                        <td className="font-mono whitespace-nowrap">{i.listen_port}</td>
                        <td className="font-mono text-[12px] whitespace-nowrap">{i.share_host || '—'}</td>
                        <td className="whitespace-nowrap">
                          <Badge color={ds.color}>{ds.label}</Badge>
                          {i.last_error ? (
                            <div className="text-[11px] text-amber-600 mt-0.5 max-w-[180px] truncate" title={i.last_error}>
                              {i.last_error}
                            </div>
                          ) : null}
                        </td>
                        <td className="text-[12px] font-mono whitespace-nowrap">
                          {probing && !latR ? (
                            <span className="text-ink-mut">…</span>
                          ) : !latR ? (
                            <button
                              type="button"
                              className="text-emerald-600 font-semibold"
                              disabled={probing}
                              onClick={() => probeLatency({ mode: 'public' })}
                              title="点击测公网延迟"
                            >
                              测延迟
                            </button>
                          ) : latR.ok ? (
                            <button
                              type="button"
                              className="text-emerald-700 dark:text-emerald-300 font-semibold tabular-nums"
                              disabled={probing}
                              onClick={() => probeLatency({ mode: 'public' })}
                              title={latR.target ? `目标 ${latR.target} · 点击重测` : '点击重测'}
                            >
                              {latR.latency_ms} ms
                            </button>
                          ) : (
                            <button
                              type="button"
                              className="text-rose-600 font-semibold"
                              disabled={probing}
                              onClick={() => probeLatency({ mode: 'public' })}
                              title={latR.error || '失败 · 点击重测'}
                            >
                              失败
                            </button>
                          )}
                        </td>
                        <td className="whitespace-nowrap">
                          {i.uri ? (
                            <CopyText text={i.uri}>
                              <span className="text-emerald-600 font-semibold text-[13px]">复制</span>
                            </CopyText>
                          ) : (
                            <span className="text-ink-mut">—</span>
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
            </div>
          )}
        </Panel>
      </div>
    </Layout>
  )
}
