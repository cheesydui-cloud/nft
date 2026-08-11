import { useState, useEffect, useMemo } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api } from '../../lib/api'
import { Layout, useToast } from '../../components/Layout'
import { Loading, Badge, Select } from '../../components/ui'
import { PageHeader, Panel } from '../../components/page'
import { copyToClipboard } from '../../lib/clipboard'
import {
  REALITY_DOMAIN_POOL,
  REALITY_FP_OPTIONS,
  SECURITY_OPTIONS,
  networksForSecurity,
  visionAllowed,
} from '../../lib/realityDomains'

const STEPS = ['选协议模板', '协议配置', '选择节点', '发布', '完成']

const TEMPLATES = [
	  { protocol: 'vless', core: 'xray', title: 'VLESS', desc: 'REALITY / TLS / 多传输，默认 REALITY 抗封锁' },
	  { protocol: 'shadowsocks', core: 'sing-box', title: 'Shadowsocks', desc: 'SS2022 / sing-box，双栈监听，客户端生态最广' },
	  { protocol: 'mieru', core: 'mieru', title: 'mieru', desc: '多路复用抗探测，TCP/UDP 双传输' },
	  { protocol: 'socks5', core: 'sing-box', title: 'SOCKS5', desc: '标准 SOCKS5 服务端 · sing-box · 可给规则/客户端当上游' },
	  { protocol: 'anytls', core: 'sing-box', title: 'AnyTLS', desc: 'TLS 隧道 · sing-box ≥1.12（协议原版推荐实现）' },
	  { protocol: 'naive', core: 'sing-box', title: 'NaiveProxy', desc: 'HTTP/2·3 代理 · sing-box 协议兼容（非 Caddy 原版前端）' },
	]

// Aligned with yyds / production sing-box SS: SS2022 first, legacy AEAD for old clients.
const SS_METHODS = [
  { value: '2022-blake3-aes-128-gcm', label: '2022-blake3-aes-128-gcm（推荐）' },
  { value: '2022-blake3-aes-256-gcm', label: '2022-blake3-aes-256-gcm' },
  { value: '2022-blake3-chacha20-poly1305', label: '2022-blake3-chacha20-poly1305' },
  { value: 'aes-128-gcm', label: 'aes-128-gcm（旧客户端）' },
  { value: 'aes-256-gcm', label: 'aes-256-gcm（旧客户端）' },
  { value: 'chacha20-ietf-poly1305', label: 'chacha20-ietf-poly1305（旧客户端）' },
]

const emptyVless = () => ({
  listen_port: 443,
  share_host: '',
  server_name: '',
  server_port: 443,
  fingerprint: 'chrome',
  network: 'tcp',
  flow: 'xtls-rprx-vision',
  max_time_difference: 60000,
  security: 'reality',
  private_key: '',
  public_key: '',
  short_id: '',
  allow_empty_short_id: false,
  encryption: '',
  decryption: '',
  uuid: '',
  sniffing: true,
  tcp_fast_open: false,
  path: '/',
  host: '',
  spider_x: '',
  xhttp_mode: 'auto',
  service_name: 'GunService',
  alpn: '',
  allow_insecure: false,
  cert_pem: '',
  key_pem: '',
})

const emptySS = () => ({
  listen_port: 443,
  share_host: '',
  method: '2022-blake3-aes-128-gcm',
  password: '',
  listen: '::',
  ntp: true,
  multiplex: false,
  tcp_fast_open: false,
  sniffing: true,
})

const emptyMieru = () => ({
	  listen_port: 443,
	  share_host: '',
	  transports: ['TCP', 'UDP'],
	  traffic_pattern: '',
	  user_hint_is_mandatory: false,
	  username: '',
	  password: '',
	})

	const emptySocks5 = () => ({
	  listen_port: 1080,
	  share_host: '',
	  listen: '::',
	  auth_mode: 'password',
	  username: '',
	  password: '',
	  udp: true,
	  ntp: true,
	  sniffing: true,
	  tcp_fast_open: false,
	})

	const emptyAnyTLS = () => ({
	  listen_port: 443,
	  share_host: '',
	  listen: '::',
	  username: 'default',
	  password: '',
	  server_name: '',
	  fingerprint: 'chrome',
	  alpn: '',
	  allow_insecure: false,
	  cert_pem: '',
	  key_pem: '',
	  ntp: true,
	  sniffing: true,
	  tcp_fast_open: false,
	})

	const emptyNaive = () => ({
	  listen_port: 443,
	  share_host: '',
	  listen: '::',
	  network: 'tcp',
	  username: '',
	  password: '',
	  server_name: '',
	  alpn: '',
	  allow_insecure: false,
	  cert_pem: '',
	  key_pem: '',
	  quic_congestion_control: '',
	  ntp: true,
	  sniffing: true,
	  tcp_fast_open: false,
	})

	function configForProtocol(p) {
	  switch (p) {
	    case 'vless': return emptyVless()
	    case 'shadowsocks': return emptySS()
	    case 'mieru': return emptyMieru()
	    case 'socks5': return emptySocks5()
	    case 'anytls': return emptyAnyTLS()
	    case 'naive': return emptyNaive()
	    default: return emptyVless()
	  }
	}


/** Lightweight form section for wizard protocol config */
function FormSection({ title, hint, children, collapsible = false, defaultOpen = true }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="rounded-[12px] border border-line bg-surface overflow-hidden">
      <div
        className={`flex items-start justify-between gap-3 px-4 py-3 ${collapsible ? 'cursor-pointer select-none' : ''}`}
        onClick={collapsible ? () => setOpen(o => !o) : undefined}
        role={collapsible ? 'button' : undefined}
      >
        <div className="min-w-0">
          <div className="text-[13.5px] font-bold text-ink">{title}</div>
          {hint && <p className="text-[11.5px] text-ink-mut mt-0.5 m-0 leading-relaxed">{hint}</p>}
        </div>
        {collapsible && (
          <svg className={`w-4 h-4 text-ink-mut shrink-0 mt-0.5 transition-transform ${open ? '' : '-rotate-90'}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m6 9 6 6 6-6"/></svg>
        )}
      </div>
      {open && <div className="px-4 pb-4 space-y-3 border-t border-line-soft pt-3">{children}</div>}
    </div>
  )
}

function coreMatches(cores, want) {
  const w = (want || '').toLowerCase()
  return (cores || []).some(c => {
    const n = (c.name || '').toLowerCase()
    if (w === 'mieru') return n === 'mieru' || n === 'mita' || n === 'mbox'
    if (w === 'sing-box') return n === 'sing-box' || n === 'singbox'
    return n === w
  })
}

export default function ProxyServiceWizard() {
  const { id: editId } = useParams()
  const isEdit = editId && editId !== 'new'
  const navigate = useNavigate()
  const toast = useToast()

  const [step, setStep] = useState(0)
  const [protocol, setProtocol] = useState('vless')
  const [core, setCore] = useState('xray')
  const [name, setName] = useState('')
  const [subVisible, setSubVisible] = useState(true)
  const [config, setConfig] = useState(emptyVless())
  const [serviceId, setServiceId] = useState(isEdit ? Number(editId) : null)
  const [nodes, setNodes] = useState([])
  const [nodeCores, setNodeCores] = useState({})
  const [selected, setSelected] = useState(new Set())
  const [publishing, setPublishing] = useState(false)
  const [result, setResult] = useState(null)
  const [loading, setLoading] = useState(!!isEdit)

  useEffect(() => {
    api.get('/nodes').then(d => {
      setNodes((d?.nodes || []).filter(n => n.node_type !== 'composite'))
      setNodeCores(d?.node_cores || {})
    }).catch(() => {})
  }, [])

  useEffect(() => {
    if (!isEdit) return
    api.get(`/proxy-services/${editId}`).then(d => {
      const s = d?.service
      if (!s) return
      setServiceId(s.id)
      setName(s.name || '')
      setProtocol(s.protocol)
      setCore(s.core)
      setSubVisible(!!s.sub_visible)
      try {
        const cfg = typeof s.config_json === 'string' ? JSON.parse(s.config_json) : (s.config_json || {})
        // REALITY 不支持 ws/httpupgrade；仅在 security=reality 时强制回 tcp
        const sec = (cfg.security || 'reality').toLowerCase()
        if (s.protocol === 'vless' && cfg && sec === 'reality') {
          const n = (cfg.network || 'tcp').toLowerCase()
          if (n === 'ws' || n === 'httpupgrade' || n === 'websocket') {
            cfg.network = 'tcp'
            if (!cfg.flow || cfg.flow === 'none') cfg.flow = 'xtls-rprx-vision'
          }
        }
        setConfig(cfg)
      } catch { /* keep */ }
      setStep(1)
    }).catch(err => toast(err.message, 'error')).finally(() => setLoading(false))
  }, [editId])

  const pickTemplate = (t) => {
	    setProtocol(t.protocol)
	    setCore(t.core)
	    setConfig(configForProtocol(t.protocol))
	    setStep(1)
	  }

	  const readPemFile = (file, key) => {
	    if (!file) return
	    const reader = new FileReader()
	    reader.onload = () => setCfg(key, String(reader.result || ''))
	    reader.readAsText(file)
	  }

  const setCfg = (key, val) => setConfig(c => ({ ...c, [key]: val }))

  const [probeNodeId, setProbeNodeId] = useState('')
  const [probingDest, setProbingDest] = useState(false)
  const [destProbe, setDestProbe] = useState(null)
  const [genEncBusy, setGenEncBusy] = useState(false)
  const [acmeBusy, setAcmeBusy] = useState(false)
  const [acmeStaging, setAcmeStaging] = useState(false)

  const onlineNodes = useMemo(
    () => nodes.filter(n => n.online === 1 || n.online === true),
    [nodes],
  )

  const issueACME = async () => {
    const sn = (config.server_name || '').trim()
    if (!sn) {
      toast('请先填写域名（server_name / SNI）', 'error')
      return
    }
    // VLESS ACME also flips security=tls; AnyTLS/Naive already TLS-only.
    const isVless = protocol === 'vless'
    // Need a saved service id to attach cert to config_json.
    let sid = serviceId
    if (!sid) {
      try {
        // Save draft first so ACME has a service row.
        const cfg = {
          ...config,
          server_name: sn,
          ...(isVless ? { security: 'tls' } : {}),
        }
        // Prefer share_host = domain when empty (import-friendly).
        if (!(cfg.share_host || '').trim()) cfg.share_host = sn
        const body = {
          name: name.trim() || `${protocol}-${Date.now()}`,
          protocol,
          core,
          sub_visible: subVisible,
          config_json: cfg,
        }
        const d = await api.post('/proxy-services', body)
        sid = d?.service?.id || d?.id
        if (sid) {
          setServiceId(sid)
          setName(body.name)
        }
      } catch (err) {
        toast(err.message || '保存服务失败，无法申请证书', 'error')
        return
      }
    }
    if (!sid) {
      toast('无法创建服务，请先保存后再申请 ACME', 'error')
      return
    }
    setAcmeBusy(true)
    try {
      // Persist current form (except empty PEMs) before issue.
      const patchCfg = {
        ...config,
        server_name: sn,
        ...(isVless ? { security: 'tls' } : {}),
        cert_pem: (config.cert_pem || '').trim() || undefined,
        key_pem: (config.key_pem || '').trim() || undefined,
      }
      if (!(patchCfg.share_host || '').trim()) patchCfg.share_host = sn
      await api.patch(`/proxy-services/${sid}`, {
        name: name.trim() || undefined,
        sub_visible: subVisible,
        config_json: patchCfg,
      }).catch(() => {})
      const d = await api.post(`/proxy-services/${sid}/acme`, {
        domain: sn,
        staging: acmeStaging,
        republish: true,
      })
      const svcCfg = d?.service?.config_json
      let next = {
        ...config,
        server_name: sn,
        share_host: (config.share_host || '').trim() || sn,
        ...(isVless ? { security: 'tls' } : {}),
      }
      if (svcCfg) {
        try {
          const parsed = typeof svcCfg === 'string' ? JSON.parse(svcCfg) : svcCfg
          next = {
            ...next,
            ...parsed,
            // redacted response: PEMs empty; keep cert_info flags
            cert_pem: '',
            key_pem: '',
            cert_configured: true,
            key_configured: true,
            cert_info: d.cert_info || parsed.cert_info || null,
            acme_enabled: true,
            acme_domain: d.domain || sn,
            acme_not_after: d.not_after || parsed.acme_not_after,
            acme_issuer: d.issuer || parsed.acme_issuer,
            acme_last_error: '',
          }
        } catch { /* keep */ }
      } else if (d.cert_info) {
        next.cert_configured = true
        next.key_configured = true
        next.cert_info = d.cert_info
        next.acme_enabled = true
        next.acme_domain = d.domain || sn
        next.acme_not_after = d.not_after
        next.acme_issuer = d.issuer
        next.acme_last_error = ''
      }
      setConfig(next)
      toast(
        d.staging
          ? `Staging 证书已签发（${d.not_after || ''}）${d.publish_note ? ' · ' + d.publish_note : ''}`
          : `Let's Encrypt 证书已签发${d.publish_note ? ' · ' + d.publish_note : ''}`,
      )
    } catch (err) {
      toast(err.message || 'ACME 申请失败', 'error')
    } finally {
      setAcmeBusy(false)
    }
  }

  const genKeys = async (kind) => {
    try {
      if (kind === 'vlessenc') setGenEncBusy(true)
      // vlessenc: default auth=x25519 (short keys, matches Weir). Use kind=mlkem for PQ.
      let q = `kind=${kind || 'reality'}`
      if (kind === 'vlessenc') q = 'kind=vlessenc&auth=x25519'
      else if (kind === 'mlkem') q = 'kind=vlessenc&auth=mlkem'
      else if (kind === 'selfsigned' || kind === 'tls') {
        const sn = (config.server_name || '').trim()
        if (!sn) {
          toast('请先填写域名（server_name），再生成自签证书', 'error')
          return
        }
        q = `kind=selfsigned&server_name=${encodeURIComponent(sn)}&days=365`
      }
      const d = await api.get(`/proxy-services/gen-keys?${q}`)
      if (kind === 'short_id') {
        setCfg('short_id', d.short_id)
        toast('已生成 short_id')
      } else if (kind === 'selfsigned' || kind === 'tls') {
        setConfig(prev => ({
          ...prev,
          cert_pem: d.cert_pem || '',
          key_pem: d.key_pem || '',
          cert_info: d.cert_info || null,
          cert_configured: true,
          key_configured: true,
          // Self-signed almost always needs client skip-verify for quick test.
          allow_insecure: true,
        }))
        toast(d.warning || '已生成自签证书（调试用，已勾选 allowInsecure）')
      } else if (kind === 'vlessenc' || kind === 'mlkem') {
        const stripQ = (s) => {
          let v = String(s || '').trim()
          while (
            (v.startsWith('"') && v.endsWith('"')) ||
            (v.startsWith("'") && v.endsWith("'"))
          ) {
            v = v.slice(1, -1).trim()
          }
          while (v.startsWith('"') || v.startsWith("'")) v = v.slice(1).trim()
          while (v.endsWith('"') || v.endsWith("'")) v = v.slice(0, -1).trim()
          while (v.endsWith(',')) v = v.slice(0, -1).trim()
          return v
        }
        // Client = 0rtt/1rtt; server = ticket lifetime (e.g. 600s). Never trust API order alone.
        let enc = stripQ(d.encryption)
        let dec = stripQ(d.decryption)
        const looksClient = (s) => {
          const p = String(s || '').toLowerCase().split('.')
          return p.length >= 3 && (p[2] === '0rtt' || p[2] === '1rtt')
        }
        const looksServer = (s) => {
          const p = String(s || '').toLowerCase().split('.')
          if (p.length < 3) return false
          if (p[2] === '0rtt' || p[2] === '1rtt') return false
          return p[2].endsWith('s') || p[2].includes('-')
        }
        if ((looksServer(enc) && looksClient(dec)) || (looksClient(dec) && !looksClient(enc))) {
          ;[enc, dec] = [dec, enc]
        }
        if (!looksClient(enc) || !looksServer(dec)) {
          toast('vlessenc 密钥角色异常（encryption 应含 0rtt，decryption 应含 600s）', 'error')
        }
        // Guard: X25519 encryption is ~80 chars; PQ is 1500+. Warn if unexpected long default.
        if ((d.auth === 'x25519' || !d.auth) && enc.length > 200) {
          toast('生成结果异常偏长（可能取了 PQ 密钥）。请再点一次生成，或清空改用 none', 'error')
        }
        setCfg('encryption', enc)
        setCfg('decryption', dec)
        const authHint = d.auth === 'mlkem' ? 'ML-KEM PQ' : 'X25519 短密钥'
        toast(d.xray_version
          ? `已生成 vlessenc（${authHint} · xray ${d.xray_version}）`
          : `已生成 vlessenc（${authHint}）`)
      } else {
        setCfg('private_key', d.private_key)
        setCfg('public_key', d.public_key)
        toast('已生成 REALITY 密钥对')
      }
    } catch (err) { toast(err.message, 'error') }
    finally { if (kind === 'vlessenc') setGenEncBusy(false) }
  }

  const probeDest = async () => {
    const sni = (config.server_name || '').trim()
    if (!sni) { toast('请先填写 server-name（SNI / dest）', 'error'); return }
    const port = Number(config.server_port) || 443
    const target = `${sni}:${port}`
    const nodeId = probeNodeId || (onlineNodes[0] && String(onlineNodes[0].id)) || ''
    setProbingDest(true)
    setDestProbe(null)
    try {
      const q = new URLSearchParams({ target, mode: 'tls', server_name: sni })
      if (nodeId) q.set('node', nodeId)
      const d = await api.get(`/probe?${q.toString()}`)
      setDestProbe(d)
      if (d.ok && (d.score === 'good' || d.score === 'ok')) {
        toast(d.summary || 'dest 探测通过', 'success')
      } else if (d.ok) {
        toast(d.summary || 'dest 可用但非最优', 'error')
      } else {
        toast(d.error || d.summary || 'dest 探测失败', 'error')
      }
    } catch (err) {
      setDestProbe({ ok: false, error: err.message, score: 'fail' })
      toast(err.message, 'error')
    } finally {
      setProbingDest(false)
    }
  }

  const saveConfig = async () => {
    if (!name.trim()) { toast('请填写名称', 'error'); return false }
    if (protocol === 'vless') {
      const sec = (config.security || 'reality').toLowerCase()
      if (sec === 'reality' && !(config.server_name || '').trim()) {
        toast('请填写 server-name（REALITY 回源 SNI），可从域名池选择', 'error')
        return false
      }
      if (sec === 'tls') {
        if (!(config.server_name || '').trim()) {
          toast('TLS 需要填写域名（SNI / 证书域名）', 'error')
          return false
        }
        const hasPEM = !!(config.cert_pem || '').trim() && !!(config.key_pem || '').trim()
        const kept = !!(config.cert_configured && config.key_configured)
        if (!hasPEM && !kept) {
          toast('TLS 需要粘贴/上传证书与私钥，或使用「生成自签证书」', 'error')
          return false
        }
      }
    }
    const body = {
      name: name.trim(),
      protocol,
      core,
      config: { ...config, listen_port: Number(config.listen_port) || 443 },
      sub_visible: subVisible,
    }
    try {
      if (serviceId) {
        await api.patch(`/proxy-services/${serviceId}`, body)
      } else {
        const d = await api.post('/proxy-services', body)
        setServiceId(d.service.id)
      }
      return true
    } catch (err) {
      toast(err.message, 'error')
      return false
    }
  }

  const eligibleNodes = useMemo(() => {
    return nodes.filter(n => {
      const cores = nodeCores[n.id] || []
      // Allow selecting even without cores (panel may push core from cache); prefer those with core.
      return !n.disabled
    })
  }, [nodes, nodeCores])

  const toggleNode = (id) => {
    setSelected(prev => {
      const n = new Set(prev)
      if (n.has(id)) n.delete(id)
      else n.add(id)
      return n
    })
  }

  const publish = async () => {
    if (!serviceId) {
      if (!(await saveConfig())) return
    }
    if (selected.size === 0) { toast('请至少选择一个节点', 'error'); return }
    setPublishing(true)
    try {
      // Persist config once more before publish.
      await saveConfig()
      const d = await api.post(`/proxy-services/${serviceId}/publish`, {
        node_ids: [...selected],
      })
      setResult(d)
      setStep(4)
      toast('发布完成')
    } catch (err) {
      toast(err.message, 'error')
    } finally {
      setPublishing(false)
    }
  }

  const syncRepo = async () => {
    if (!serviceId) return
    try {
      const d = await api.post(`/proxy-services/${serviceId}/sync-repo`, {})
      toast(`已同步 ${d.synced || 0} 条到落地仓库`)
    } catch (err) { toast(err.message, 'error') }
  }

  if (loading) return <Layout><Loading /></Layout>

  return (
    <Layout>
      {/* min-h-0 + flex-1: fill main column; internal scroll so long VLESS form
          is not clipped by Panel's overflow-hidden. Sticky footer keeps 下一步 visible. */}
      <div className="h-full min-h-0 flex flex-col max-w-5xl">
        <PageHeader title={isEdit ? '编辑代理服务' : '发布服务'}
          actions={<button type="button" className="btn-secondary" onClick={() => navigate('/proxy-services')}>返回列表</button>}
        />

        {/* Steps */}
        <div className="flex flex-wrap gap-2 mb-4 shrink-0">
          {STEPS.map((label, i) => (
            <button key={label} type="button"
              onClick={() => { if (i < step || (i <= 1 && serviceId)) setStep(i) }}
              className={`px-3 py-1.5 rounded-lg text-[13px] font-semibold border transition-colors ${
                i === step
                  ? 'bg-emerald-50 text-emerald-700 border-emerald-300 dark:bg-emerald-900/30 dark:text-emerald-300 dark:border-emerald-700'
                  : i < step
                    ? 'bg-surface text-ink border-line hover:border-emerald-400'
                    : 'bg-surface text-ink-mut border-line opacity-60'
              }`}>
              {i + 1}. {label}
            </button>
          ))}
        </div>

        <Panel fill className="min-h-0">
          <div className="flex-1 min-h-0 overflow-y-auto px-6 py-5">
          {step === 0 && (
            <div>
              <h3 className="text-sm font-bold mb-3">选协议模板</h3>
              <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-3">
                {TEMPLATES.map(t => (
                  <button key={t.protocol} type="button" onClick={() => pickTemplate(t)}
                    className="text-left p-4 rounded-xl border border-line bg-surface hover:border-emerald-500 hover:shadow-sm transition-all">
                    <div className="font-bold text-[15px]">{t.title}</div>
                    <div className="text-[12.5px] text-ink-mut mt-1.5 leading-relaxed">{t.desc}</div>
                    <div className="text-[11px] font-mono text-ink-mut mt-2">核心 · {t.core}</div>
                  </button>
                ))}
              </div>
            </div>
          )}

          {step === 1 && (
            <div className="space-y-4">
              <div className="grid sm:grid-cols-2 gap-3">
                <div>
                  <label className="fl block mb-1">协议</label>
                  <Select value={protocol} onChange={v => {
                    setProtocol(v)
                    setCore(TEMPLATES.find(t => t.protocol === v)?.core || core)
                    setConfig(configForProtocol(v))
                  }} options={TEMPLATES.map(t => ({ value: t.protocol, label: t.title }))} />
                </div>
                <div>
                  <label className="fl block mb-1">核心</label>
                  <input className="input-field font-mono" value={core} readOnly />
                </div>
                <div>
                  <label className="fl block mb-1">名称</label>
                  <input className="input-field" value={name} onChange={e => setName(e.target.value)} placeholder="例如：瓦工 / gen2" />
                </div>
                <div>
                  <label className="fl block mb-1">默认端口</label>
                  <input className="input-field font-mono" type="number" value={config.listen_port || 443}
                    onChange={e => setCfg('listen_port', Number(e.target.value) || 443)} />
                  <p className="text-[11px] text-ink-mut mt-1">部署时预填，可在部署页修改</p>
                </div>
              </div>

              {protocol === 'vless' && (() => {
                const sec = (config.security || 'reality').toLowerCase()
                const netOpts = networksForSecurity(sec)
                const netw = (config.network || 'tcp').toLowerCase()
                const canVision = visionAllowed(sec, netw)
                const hasAdvEnc = !!(config.encryption || config.decryption)
                const hasAdvReality = sec === 'reality' && (
                  (config.max_time_difference != null && config.max_time_difference !== 60000)
                  || !!config.spider_x
                  || !!config.allow_empty_short_id
                )
                const hasAdvCommon = config.sniffing === false || !!config.tcp_fast_open
                const setSecurity = (v) => {
                  const next = (v || 'reality').toLowerCase()
                  const allowed = networksForSecurity(next).map(o => o.value)
                  let n = (config.network || 'tcp').toLowerCase()
                  if (!allowed.includes(n)) n = 'tcp'
                  const patch = { security: next, network: n }
                  if (visionAllowed(next, n)) {
                    if (!config.flow || config.flow === 'none') patch.flow = 'xtls-rprx-vision'
                  } else {
                    patch.flow = 'none'
                  }
                  setConfig(prev => ({ ...prev, ...patch }))
                }
                const setNetwork = (v) => {
                  const n = v || 'tcp'
                  const patch = { network: n }
                  if (visionAllowed(sec, n)) {
                    if (!config.flow || config.flow === 'none') patch.flow = 'xtls-rprx-vision'
                  } else {
                    patch.flow = 'none'
                  }
                  if (n === 'grpc' && !config.service_name) patch.service_name = 'GunService'
                  if ((n === 'ws' || n === 'httpupgrade' || n === 'xhttp') && !config.path) patch.path = '/'
                  setConfig(prev => ({ ...prev, ...patch }))
                }
                const readPemFile = (file, key) => {
                  if (!file) return
                  const reader = new FileReader()
                  reader.onload = () => {
                    const text = String(reader.result || '')
                    setCfg(key, text)
                    toast(`已读入 ${file.name}`, 'success')
                  }
                  reader.onerror = () => toast('读取文件失败', 'error')
                  reader.readAsText(file)
                }
                const transportParams = (netw === 'xhttp' || netw === 'ws' || netw === 'httpupgrade' || netw === 'grpc')
                return (
                <div className="space-y-3 border-t border-line pt-4">
                  {/* ① 安全层 */}
                  <FormSection title="安全层" hint="决定证书与传输矩阵。默认 REALITY（免证书、抗封锁）。">
                    <Select value={sec} onChange={setSecurity} options={SECURITY_OPTIONS} />
                    {sec === 'none' && (
                      <div className="rounded-lg border border-amber-300 bg-amber-50 text-amber-900 dark:bg-amber-900/20 dark:text-amber-100 dark:border-amber-700 px-3 py-2 text-[12.5px]">
                        <strong>警告：</strong>安全层为「无」时流量明文传输，请勿暴露在公网。
                      </div>
                    )}
                  </FormSection>

                  {/* ② REALITY 回源 */}
                  {sec === 'reality' && (
                    <FormSection title="回源目标" hint="REALITY dest：客户端 SNI / 服务端回源站点，上线前建议探测 TLS1.3 + h2。">
                      <div className="grid sm:grid-cols-2 gap-3">
                        <div className="sm:col-span-2">
                          <label className="fl block mb-1">server-name（回源 / SNI）</label>
                          <input className="input-field font-mono" value={config.server_name || ''} onChange={e => setCfg('server_name', e.target.value)}
                            placeholder="cdn.example.com" />
                        </div>
                        <div>
                          <label className="fl block mb-1">域名池（可选）</label>
                          <Select
                            value={config.server_name && REALITY_DOMAIN_POOL.some(d => d.domain === config.server_name) ? config.server_name : ''}
                            onChange={v => { if (v) setCfg('server_name', v) }}
                            options={[
                              { value: '', label: '选择预置域名…' },
                              ...REALITY_DOMAIN_POOL.map(d => ({
                                value: d.domain,
                                label: `${d.flag} ${d.domain} · ${d.label}`,
                              })),
                            ]}
                          />
                        </div>
                        <div>
                          <label className="fl block mb-1">server-port（回源）</label>
                          <input className="input-field font-mono" type="number" value={config.server_port || 443}
                            onChange={e => setCfg('server_port', Number(e.target.value) || 443)} />
                        </div>
                        <div className="sm:col-span-2">
                          <label className="fl block mb-1">指纹（客户端）</label>
                          <Select value={config.fingerprint || 'chrome'} onChange={v => setCfg('fingerprint', v)}
                            options={REALITY_FP_OPTIONS.map(v => ({ value: v, label: v }))} />
                          <p className="text-[11px] text-ink-mut mt-1">ClientHello 伪装；默认 chrome</p>
                        </div>
                      </div>
                      <div className="rounded-[10px] border border-line-soft bg-raised/60 px-3 py-3 space-y-2">
                        <div className="text-[12.5px] font-semibold text-ink-soft">探测 dest</div>
                        <div className="grid sm:grid-cols-2 gap-3 items-end">
                          <div>
                            <label className="fl block mb-1">探测节点</label>
                            <Select
                              value={probeNodeId || (onlineNodes[0] ? String(onlineNodes[0].id) : '')}
                              onChange={v => setProbeNodeId(v)}
                              options={[
                                ...(onlineNodes.length === 0 ? [{ value: '', label: '无在线节点（将用面板本机）' }] : []),
                                ...onlineNodes.map(n => ({
                                  value: String(n.id),
                                  label: `${n.name || '#' + n.id}${n.region ? ' · ' + n.region : ''}`,
                                })),
                              ]}
                            />
                          </div>
                          <div>
                            <button type="button" className="btn-primary text-sm" disabled={probingDest}
                              onClick={probeDest}>{probingDest ? '探测中…' : '探测 dest'}</button>
                          </div>
                        </div>
                        {destProbe && (
                          <div className={`text-[12.5px] rounded-lg border px-3 py-2 ${
                            destProbe.score === 'good' ? 'border-emerald-300 bg-emerald-50 text-emerald-800 dark:bg-emerald-900/20 dark:text-emerald-200 dark:border-emerald-700'
                              : destProbe.score === 'ok' ? 'border-amber-300 bg-amber-50 text-amber-900 dark:bg-amber-900/20 dark:text-amber-100'
                              : 'border-rose-300 bg-rose-50 text-rose-800 dark:bg-rose-900/20 dark:text-rose-200'
                          }`}>
                            <div className="font-semibold">{destProbe.summary || (destProbe.ok ? '完成' : (destProbe.error || '失败'))}</div>
                            <div className="mt-1 font-mono text-[11px] opacity-90 space-y-0.5">
                              {destProbe.latency_ms != null && <div>延迟 {destProbe.latency_ms} ms</div>}
                              {destProbe.tls_version && <div>TLS {destProbe.tls_version}{destProbe.alpn ? ` · ALPN ${destProbe.alpn}` : ''}{destProbe.tls13 ? ' · TLS1.3✓' : ''}{destProbe.h2 ? ' · h2✓' : ''}</div>}
                              {destProbe.cert_cn && <div>证书 CN {destProbe.cert_cn}{destProbe.sni_match ? ' · SNI 匹配' : ' · SNI 未匹配'}</div>}
                              {destProbe.cipher && <div>套件 {destProbe.cipher}</div>}
                              {destProbe.error && !destProbe.ok && <div className="text-rose-700 dark:text-rose-300">{destProbe.error}</div>}
                            </div>
                          </div>
                        )}
                      </div>
                    </FormSection>
                  )}

                  {/* ② TLS 域名 */}
                  {sec === 'tls' && (
                    <FormSection title="域名" hint="须与证书 CN/SAN 一致，同时作为客户端 SNI。">
                      <div className="grid sm:grid-cols-2 gap-3">
                        <div>
                          <label className="fl block mb-1">域名（TLS SNI）</label>
                          <input className="input-field font-mono" value={config.server_name || ''} onChange={e => setCfg('server_name', e.target.value)}
                            placeholder="vpn.example.com" />
                        </div>
                        <div>
                          <label className="fl block mb-1">指纹（客户端）</label>
                          <Select value={config.fingerprint || 'chrome'} onChange={v => setCfg('fingerprint', v)}
                            options={REALITY_FP_OPTIONS.map(v => ({ value: v, label: v }))} />
                        </div>
                      </div>
                    </FormSection>
                  )}

                  {/* ③ 传输 */}
                  <FormSection
                    title="传输层"
                    hint={sec === 'reality' ? 'REALITY 仅 tcp / xhttp / gRPC（Xray 限制）' : 'TLS / 无：tcp · ws · gRPC · xhttp · HTTPUpgrade'}
                  >
                    <div className="grid sm:grid-cols-2 gap-3">
                      <div>
                        <label className="fl block mb-1">network</label>
                        <Select value={netOpts.some(o => o.value === netw) ? netw : 'tcp'} onChange={setNetwork} options={netOpts} />
                      </div>
                      <div>
                        <label className="fl block mb-1">flow</label>
                        <Select
                          value={config.flow === '' || config.flow === 'none' ? 'none' : (config.flow || 'xtls-rprx-vision')}
                          onChange={v => setCfg('flow', v === 'none' ? 'none' : v)}
                          options={[
                            { value: 'xtls-rprx-vision', label: 'xtls-rprx-vision', disabled: !canVision },
                            { value: 'none', label: '关' },
                          ].filter(o => !o.disabled || o.value === 'none')}
                        />
                        <p className="text-[11px] text-ink-mut mt-1">
                          {canVision ? 'tcp + REALITY/TLS 可用 vision' : '当前组合不支持 vision'}
                        </p>
                      </div>
                    </div>
                    {transportParams && (
                      <div className="grid sm:grid-cols-2 gap-3 pt-1">
                        {(netw === 'xhttp' || netw === 'ws' || netw === 'httpupgrade') && (
                          <>
                            <div>
                              <label className="fl block mb-1">path</label>
                              <input className="input-field font-mono" value={config.path || '/'} onChange={e => setCfg('path', e.target.value || '/')} />
                            </div>
                            <div>
                              <label className="fl block mb-1">host（可选）</label>
                              <input className="input-field font-mono" value={config.host || ''} onChange={e => setCfg('host', e.target.value)} placeholder="默认与 SNI 一致时可留空" />
                            </div>
                            {netw === 'xhttp' && (
                              <div>
                                <label className="fl block mb-1">xhttp mode</label>
                                <Select value={config.xhttp_mode || 'auto'} onChange={v => setCfg('xhttp_mode', v)}
                                  options={['auto', 'packet-up', 'stream-up', 'stream-one'].map(v => ({ value: v, label: v }))} />
                              </div>
                            )}
                          </>
                        )}
                        {netw === 'grpc' && (
                          <div className="sm:col-span-2">
                            <label className="fl block mb-1">serviceName</label>
                            <input className="input-field font-mono" value={config.service_name || 'GunService'}
                              onChange={e => setCfg('service_name', e.target.value || 'GunService')} />
                            <p className="text-[11px] text-ink-mut mt-1">默认 GunService；须与客户端一致</p>
                          </div>
                        )}
                      </div>
                    )}
                  </FormSection>

                  {/* ④ REALITY 密钥 */}
                  {sec === 'reality' && (
                    <FormSection title="REALITY 密钥" hint="一键生成后即可发布；short_id / spiderX 可按需改。">
                      <div className="flex flex-wrap gap-2">
                        <button type="button" className="btn-primary text-sm" onClick={() => genKeys('reality')}>生成密钥</button>
                        <button type="button" className="btn-secondary text-sm" onClick={() => genKeys('short_id')}>生成 short_id</button>
                      </div>
                      <div className="grid sm:grid-cols-2 gap-3">
                        <div>
                          <label className="fl block mb-1">private_key</label>
                          <input className="input-field font-mono text-xs" value={config.private_key || ''} onChange={e => setCfg('private_key', e.target.value)} />
                        </div>
                        <div>
                          <label className="fl block mb-1">public_key</label>
                          <input className="input-field font-mono text-xs" value={config.public_key || ''} onChange={e => setCfg('public_key', e.target.value)} />
                        </div>
                        <div>
                          <label className="fl block mb-1">short_id</label>
                          <input className="input-field font-mono" value={config.short_id || ''} onChange={e => setCfg('short_id', e.target.value)} />
                        </div>
                        <div>
                          <label className="fl block mb-1">spiderX（可选）</label>
                          <input className="input-field font-mono" value={config.spider_x || ''} onChange={e => setCfg('spider_x', e.target.value)} placeholder="/" />
                        </div>
                      </div>
                      <label className="inline-flex items-center gap-2 text-sm">
                        <input type="checkbox" checked={!!config.allow_empty_short_id}
                          onChange={e => setCfg('allow_empty_short_id', e.target.checked)} />
                        允许空 shortId（兼容旧客户端；默认关闭更严）
                      </label>
                    </FormSection>
                  )}

                  {/* ④ TLS 证书 */}
                  {sec === 'tls' && (
                    <FormSection title="TLS 证书" hint="推荐 Cloudflare DNS-01 一键申请；也可粘贴 PEM 或自签调试证书。">
                      {(config.cert_configured || config.key_configured || config.cert_info || config.acme_enabled) && !(config.cert_pem || '').trim() && (
                        <div className="rounded-lg border border-line bg-raised/50 px-3 py-2 text-[12.5px] space-y-1">
                          <div className="font-semibold text-ink">
                            已保存证书{config.key_configured ? '与私钥' : ''}
                            {config.acme_enabled ? ' · ACME 自动续期' : ''}
                            {!(config.cert_pem || '').trim() && '（脱敏，留空保存将保留原值）'}
                          </div>
                          {(config.cert_info?.not_after || config.acme_not_after) && (
                            <div className={`font-mono text-[11px] ${
                              config.cert_info?.expired ? 'text-rose-600' : config.cert_info?.expiring ? 'text-amber-600' : 'text-ink-mut'
                            }`}>
                              有效期至 {config.cert_info?.not_after || config.acme_not_after}
                              {config.cert_info?.expired ? ' · 已过期' : config.cert_info?.days_left != null ? ` · 剩余 ${config.cert_info.days_left} 天` : ''}
                              {config.cert_info?.cn ? ` · CN ${config.cert_info.cn}` : ''}
                              {config.acme_issuer ? ` · ${config.acme_issuer}` : ''}
                            </div>
                          )}
                          {config.cert_info?.fingerprint && (
                            <div className="font-mono text-[10px] text-ink-mut break-all">SHA256 {config.cert_info.fingerprint}</div>
                          )}
                          {config.acme_last_error && (
                            <div className="text-rose-600 text-[11px]">上次 ACME 错误：{config.acme_last_error}</div>
                          )}
                        </div>
                      )}
                      <div className="flex flex-wrap items-center gap-2">
                        <button type="button" className="btn-primary text-sm" disabled={acmeBusy} onClick={issueACME}>
                          {acmeBusy ? 'ACME 申请中…' : (config.acme_enabled || config.cert_configured ? '续期 / 重新申请 ACME' : '申请 Let\'s Encrypt（DNS-01）')}
                        </button>
                        <button type="button" className="btn-secondary text-sm" onClick={() => genKeys('selfsigned')}>
                          生成自签证书（调试）
                        </button>
                        <label className="inline-flex items-center gap-1.5 text-[12px] text-ink-soft">
                          <input type="checkbox" checked={acmeStaging} onChange={e => setAcmeStaging(e.target.checked)} />
                          Staging
                        </label>
                      </div>
                      <div>
                        <label className="fl block mb-1">证书 PEM（cert）</label>
                        <textarea className="input-field font-mono text-xs min-h-[88px]" value={config.cert_pem || ''}
                          onChange={e => setCfg('cert_pem', e.target.value)}
                          placeholder={config.cert_configured ? '已配置 · 留空保留原证书' : '-----BEGIN CERTIFICATE-----'} />
                        <input type="file" accept=".pem,.crt,.cer,.txt" className="mt-1 text-xs" onChange={e => readPemFile(e.target.files?.[0], 'cert_pem')} />
                      </div>
                      <div>
                        <label className="fl block mb-1">私钥 PEM（key）</label>
                        <textarea className="input-field font-mono text-xs min-h-[88px]" value={config.key_pem || ''}
                          onChange={e => setCfg('key_pem', e.target.value)}
                          placeholder={config.key_configured ? '已配置 · 留空保留原私钥' : '-----BEGIN PRIVATE KEY-----'} />
                        <input type="file" accept=".pem,.key,.txt" className="mt-1 text-xs" onChange={e => readPemFile(e.target.files?.[0], 'key_pem')} />
                      </div>
                      <div className="grid sm:grid-cols-2 gap-3">
                        <div>
                          <label className="fl block mb-1">ALPN（可选）</label>
                          <input className="input-field font-mono" value={config.alpn || ''} onChange={e => setCfg('alpn', e.target.value)}
                            placeholder="h2,http/1.1" />
                        </div>
                        <label className="inline-flex items-center gap-2 text-sm mt-6">
                          <input type="checkbox" checked={!!config.allow_insecure}
                            onChange={e => setCfg('allow_insecure', e.target.checked)} />
                          客户端 allowInsecure（仅调试）
                        </label>
                      </div>
                    </FormSection>
                  )}

                  {/* ⑤ 高级 */}
                  <FormSection
                    title="高级"
                    hint="VLESS Encryption、时差、嗅探与 TFO；默认无需改动。"
                    collapsible
                    defaultOpen={hasAdvEnc || hasAdvReality || hasAdvCommon}
                  >
                    {sec === 'reality' && (
                      <div>
                        <label className="fl block mb-1">max_time_difference</label>
                        <input className="input-field font-mono" type="number" value={config.max_time_difference ?? 60000}
                          onChange={e => setCfg('max_time_difference', Number(e.target.value))} />
                        <p className="text-[11px] text-ink-mut mt-1">毫秒，如 60000=1m</p>
                      </div>
                    )}
                    <div>
                      <label className="fl block mb-1">VLESS Encryption（可选）</label>
                      <div className="grid sm:grid-cols-2 gap-3 mb-2">
                        <div>
                          <label className="text-[11px] text-ink-mut block mb-1">decryption（服务端）</label>
                          <input className="input-field font-mono text-xs" value={config.decryption || ''} onChange={e => setCfg('decryption', e.target.value)}
                            placeholder="none 或 mlkem…" />
                        </div>
                        <div>
                          <label className="text-[11px] text-ink-mut block mb-1">encryption（客户端）</label>
                          <input className="input-field font-mono text-xs" value={config.encryption || ''} onChange={e => setCfg('encryption', e.target.value)}
                            placeholder="留空 = none" />
                        </div>
                      </div>
                      <div className="flex flex-wrap gap-2">
                        <button type="button" className="btn-primary text-sm" disabled={genEncBusy}
                          onClick={() => genKeys('vlessenc')}>{genEncBusy ? '生成中…' : '生成 vlessenc'}</button>
                        <button type="button" className="btn-secondary text-sm" onClick={() => {
                          setCfg('encryption', '')
                          setCfg('decryption', '')
                          toast('已清空 encryption / decryption（使用 none）')
                        }}>清空</button>
                      </div>
                      <p className="text-[11px] text-ink-mut mt-1">改后须重新发布并重新导入客户端。</p>
                    </div>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={config.sniffing !== false}
                        onChange={e => setCfg('sniffing', e.target.checked)} />
                      <span className="font-mono">sniffing</span>
                      <span className="text-ink-mut text-[12px]">入站嗅探，默认开</span>
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={!!config.tcp_fast_open}
                        onChange={e => setCfg('tcp_fast_open', e.target.checked)} />
                      <span className="font-mono">tcp_fast_open</span>
                      <span className="text-ink-mut text-[12px]">需内核开启 TFO</span>
                    </label>
                  </FormSection>
                </div>
                )
              })()}

              {protocol === 'shadowsocks' && (
                <div className="border-t border-line pt-4 space-y-3">
                  <FormSection title="加密与访问" hint="对齐生产 sing-box SS：SS2022 推荐，密码可自动生成。">
                    <div>
                      <label className="fl block mb-1">加密 method</label>
                      <Select
                        value={config.method || SS_METHODS[0].value}
                        onChange={v => setCfg('method', v)}
                        options={SS_METHODS}
                      />
                    </div>
                    <div>
                      <label className="fl block mb-1">密码（SS2022 为 base64 密钥）</label>
                      <div className="flex gap-2">
                        <input
                          className="input-field font-mono text-xs flex-1"
                          value={config.password || ''}
                          onChange={e => setCfg('password', e.target.value)}
                          placeholder={config.password_configured ? '已配置 · 留空保存保留原密码；点生成可轮换' : '留空则发布时自动生成'}
                        />
                        <button
                          type="button"
                          className="btn-secondary text-sm shrink-0"
                          onClick={async () => {
                            try {
                              const m = config.method || '2022-blake3-aes-128-gcm'
                              const d = await api.get(`/proxy-services/gen-keys?kind=ss&method=${encodeURIComponent(m)}`)
                              setCfg('password', d.password || '')
                              toast('已生成新密码（发布后生效）')
                            } catch (err) {
                              toast(err.message || '生成失败', 'error')
                            }
                          }}
                        >
                          生成密码
                        </button>
                      </div>
                    </div>
                    <div>
                      <label className="fl block mb-1">分享主机 share_host（可选）</label>
                      <input
                        className="input-field font-mono"
                        value={config.share_host || ''}
                        onChange={e => setCfg('share_host', e.target.value)}
                        placeholder="留空则用节点 IP / 中转地址"
                      />
                      <p className="text-[11px] text-ink-mut mt-1">写入订阅链接的主机名；域名或 DDNS 时填写。</p>
                    </div>
                  </FormSection>

                  <FormSection title="监听" hint="双栈 :: 推荐；仅 IPv4 时选 0.0.0.0。">
                    <div>
                      <label className="fl block mb-1">listen</label>
                      <Select
                        value={config.listen || '::'}
                        onChange={v => setCfg('listen', v)}
                        options={[
                          { value: '::', label: '::（双栈，推荐）' },
                          { value: '0.0.0.0', label: '0.0.0.0（仅 IPv4）' },
                        ]}
                      />
                    </div>
                  </FormSection>

                  <FormSection
                    title="高级"
                    hint="NTP、嗅探、TFO、multiplex；默认已适合大多数场景。"
                    collapsible
                    defaultOpen={config.ntp === false || config.sniffing === false || !!config.tcp_fast_open || !!config.multiplex}
                  >
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={config.ntp !== false} onChange={e => setCfg('ntp', e.target.checked)} />
                      <span>NTP 校时（time.apple.com，默认开）</span>
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={config.sniffing !== false} onChange={e => setCfg('sniffing', e.target.checked)} />
                      <span className="font-mono">sniff</span>
                      <span className="text-ink-mut text-[12px]">入站嗅探</span>
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={!!config.tcp_fast_open} onChange={e => setCfg('tcp_fast_open', e.target.checked)} />
                      <span className="font-mono">tcp_fast_open</span>
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={!!config.multiplex} onChange={e => setCfg('multiplex', e.target.checked)} />
                      <span>multiplex（smux）</span>
                      <span className="text-ink-mut text-[12px]">客户端需同步开 mux</span>
                    </label>
                  </FormSection>
                </div>
              )}

              {protocol === 'mieru' && (
                <div className="border-t border-line pt-4 space-y-3">
                  <FormSection title="传输" hint="至少勾选一种；TCP + UDP 双开更稳。">
                    <div className="flex gap-4">
                      {['TCP', 'UDP'].map(tr => {
                        const on = (config.transports || []).includes(tr)
                        return (
                          <label key={tr} className="inline-flex items-center gap-2 text-sm">
                            <input type="checkbox" checked={on} onChange={() => {
                              const cur = new Set(config.transports || [])
                              if (cur.has(tr)) cur.delete(tr)
                              else cur.add(tr)
                              setCfg('transports', [...cur])
                            }} />
                            {tr}
                          </label>
                        )
                      })}
                    </div>
                  </FormSection>

                  <FormSection
                    title="高级"
                    hint="流量模式与用户提示；一般保持默认。"
                    collapsible
                    defaultOpen={!!config.traffic_pattern || !!config.user_hint_is_mandatory}
                  >
                    <div>
                      <label className="fl block mb-1">traffic_pattern（可选）</label>
                      <input className="input-field font-mono" value={config.traffic_pattern || ''} onChange={e => setCfg('traffic_pattern', e.target.value)} />
                    </div>
                    <div>
                      <label className="fl block mb-1">user_hint_is_mandatory</label>
                      <Select value={config.user_hint_is_mandatory ? 'true' : 'false'}
                        onChange={v => setCfg('user_hint_is_mandatory', v === 'true')}
                        options={[{ value: 'false', label: '默认 (false)' }, { value: 'true', label: 'true' }]} />
                    </div>
                  </FormSection>
                </div>
              )}

              {protocol === 'socks5' && (
                <div className="border-t border-line pt-4 space-y-3">
                  <FormSection title="认证" hint="默认用户名密码；无认证仅建议内网使用。">
                    <div>
                      <label className="fl block mb-1">认证方式</label>
                      <Select
                        value={config.auth_mode || 'password'}
                        onChange={v => setCfg('auth_mode', v)}
                        options={[
                          { value: 'password', label: '用户名密码（推荐）' },
                          { value: 'none', label: '无认证（仅内网）' },
                        ]}
                      />
                      {(config.auth_mode || 'password') === 'none' && (
                        <p className="text-[12px] text-amber-700 dark:text-amber-300 mt-1.5 m-0">
                          无认证 SOCKS5 勿暴露公网；任何能连上的人都能代理出站。
                        </p>
                      )}
                    </div>
                    {(config.auth_mode || 'password') === 'password' && (
                      <div className="grid sm:grid-cols-2 gap-3">
                        <div>
                          <label className="fl block mb-1">用户名</label>
                          <input className="input-field font-mono" value={config.username || ''}
                            onChange={e => setCfg('username', e.target.value)}
                            placeholder="留空则发布时自动生成" />
                        </div>
                        <div>
                          <label className="fl block mb-1">密码</label>
                          <div className="flex gap-2">
                            <input className="input-field font-mono text-xs flex-1" value={config.password || ''}
                              onChange={e => setCfg('password', e.target.value)}
                              placeholder={config.password_configured ? '已配置 · 留空保留' : '留空则自动生成'} />
                            <button type="button" className="btn-secondary text-sm shrink-0"
                              onClick={() => {
                                const u = config.username || (`u${Math.random().toString(16).slice(2, 6)}`)
                                const p = Array.from(crypto.getRandomValues(new Uint8Array(12)))
                                  .map(b => b.toString(16).padStart(2, '0')).join('')
                                setConfig(c => ({ ...c, username: u, password: p }))
                                toast('已生成账密')
                              }}>生成</button>
                          </div>
                        </div>
                      </div>
                    )}
                    <div>
                      <label className="fl block mb-1">分享主机 share_host（可选）</label>
                      <input className="input-field font-mono" value={config.share_host || ''}
                        onChange={e => setCfg('share_host', e.target.value)}
                        placeholder="留空则用节点 IP / 中转地址" />
                    </div>
                  </FormSection>
                  <FormSection title="监听" hint="双栈 :: 推荐。">
                    <Select value={config.listen || '::'} onChange={v => setCfg('listen', v)}
                      options={[
                        { value: '::', label: '::（双栈，推荐）' },
                        { value: '0.0.0.0', label: '0.0.0.0（仅 IPv4）' },
                      ]} />
                  </FormSection>
                  <FormSection title="高级" collapsible
                    defaultOpen={config.udp === false || config.sniffing === false || !!config.tcp_fast_open || config.ntp === false}>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={config.udp !== false} onChange={e => setCfg('udp', e.target.checked)} />
                      <span>UDP ASSOCIATE</span>
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={config.ntp !== false} onChange={e => setCfg('ntp', e.target.checked)} />
                      <span>NTP 校时</span>
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={config.sniffing !== false} onChange={e => setCfg('sniffing', e.target.checked)} />
                      <span className="font-mono">sniff</span>
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={!!config.tcp_fast_open} onChange={e => setCfg('tcp_fast_open', e.target.checked)} />
                      <span className="font-mono">tcp_fast_open</span>
                    </label>
                  </FormSection>
                </div>
              )}

              {protocol === 'anytls' && (
                <div className="border-t border-line pt-4 space-y-3">
                  <FormSection title="访问与密钥" hint="AnyTLS 密码认证；用户名仅写入 sing-box users.name。">
                    <div className="grid sm:grid-cols-2 gap-3">
                      <div>
                        <label className="fl block mb-1">用户名（可选）</label>
                        <input className="input-field font-mono" value={config.username || 'default'}
                          onChange={e => setCfg('username', e.target.value)} />
                      </div>
                      <div>
                        <label className="fl block mb-1">密码</label>
                        <div className="flex gap-2">
                          <input className="input-field font-mono text-xs flex-1" value={config.password || ''}
                            onChange={e => setCfg('password', e.target.value)}
                            placeholder={config.password_configured ? '已配置 · 留空保留' : '留空则自动生成'} />
                          <button type="button" className="btn-secondary text-sm shrink-0"
                            onClick={() => {
                              const bytes = crypto.getRandomValues(new Uint8Array(16))
                              const p = btoa(String.fromCharCode(...bytes))
                              setCfg('password', p)
                              toast('已生成密码')
                            }}>生成</button>
                        </div>
                      </div>
                    </div>
                    <div>
                      <label className="fl block mb-1">分享主机 share_host（可选）</label>
                      <input className="input-field font-mono" value={config.share_host || ''}
                        onChange={e => setCfg('share_host', e.target.value)}
                        placeholder="域名或 IP；建议填证书域名" />
                    </div>
                  </FormSection>
                  <FormSection title="域名与证书" hint="TLS 必填。推荐 ACME 一键；调试可用自签（客户端须勾选 insecure）。">
                    <div className="grid sm:grid-cols-2 gap-3">
                      <div>
                        <label className="fl block mb-1">域名（SNI）*</label>
                        <input className="input-field font-mono" value={config.server_name || ''}
                          onChange={e => {
                            const v = e.target.value
                            setConfig(c => ({
                              ...c,
                              server_name: v,
                              // 同步空 share_host，订阅主机名与证书域名一致
                              share_host: (c.share_host || '').trim() ? c.share_host : v,
                            }))
                          }} placeholder="vpn.example.com" />
                        <p className="text-[11px] text-ink-mut mt-1">须解析到节点 IP（或中转入口）；证书 SAN 须覆盖此域名。</p>
                      </div>
                      <div>
                        <label className="fl block mb-1">客户端指纹</label>
                        <Select value={config.fingerprint || 'chrome'} onChange={v => setCfg('fingerprint', v)}
                          options={REALITY_FP_OPTIONS.map(v => ({ value: v, label: v }))} />
                      </div>
                    </div>
                    {(config.cert_configured || config.key_configured || config.cert_info || config.acme_enabled) && !(config.cert_pem || '').trim() && (
                      <div className="rounded-lg border border-line bg-raised/50 px-3 py-2 text-[12.5px] space-y-1">
                        <div className="font-semibold text-ink">
                          已保存证书{config.key_configured ? '与私钥' : ''}
                          {config.acme_enabled ? ' · ACME 自动续期' : ''}
                          {'（脱敏，留空保存将保留原值）'}
                        </div>
                        {(config.cert_info?.not_after || config.acme_not_after) && (
                          <div className="font-mono text-[11px] text-ink-mut">
                            有效期至 {config.cert_info?.not_after || config.acme_not_after}
                            {config.acme_issuer ? ` · ${config.acme_issuer}` : ''}
                          </div>
                        )}
                        {config.acme_last_error && (
                          <div className="text-rose-600 text-[11px]">上次 ACME 错误：{config.acme_last_error}</div>
                        )}
                      </div>
                    )}
                    <div className="flex flex-wrap items-center gap-2">
                      <button type="button" className="btn-primary text-sm" disabled={acmeBusy} onClick={issueACME}>
                        {acmeBusy ? 'ACME 申请中…' : (config.acme_enabled || config.cert_configured ? '续期 / 重新申请 ACME' : '申请 Let\'s Encrypt（DNS-01）')}
                      </button>
                      <button type="button" className="btn-secondary text-sm" onClick={() => genKeys('selfsigned')}>
                        生成自签证书（调试）
                      </button>
                      <label className="inline-flex items-center gap-1.5 text-[12px] text-ink-soft">
                        <input type="checkbox" checked={acmeStaging} onChange={e => setAcmeStaging(e.target.checked)} />
                        Staging
                      </label>
                    </div>
                    <p className="text-[11.5px] text-ink-mut m-0 leading-relaxed">
                      ACME 需系统设置里配置 Cloudflare API Token（DNS-01）。域名须在该 CF 账号下。自签仅调试，客户端要勾选 insecure。
                    </p>
                    <div>
                      <label className="fl block mb-1">证书 PEM</label>
                      <textarea className="input-field font-mono text-xs min-h-[88px]" value={config.cert_pem || ''}
                        onChange={e => setCfg('cert_pem', e.target.value)}
                        placeholder={config.cert_configured ? '已配置 · 留空保留原证书' : '-----BEGIN CERTIFICATE-----'} />
                      <input type="file" accept=".pem,.crt,.cer,.txt" className="mt-1 text-xs"
                        onChange={e => readPemFile(e.target.files?.[0], 'cert_pem')} />
                    </div>
                    <div>
                      <label className="fl block mb-1">私钥 PEM</label>
                      <textarea className="input-field font-mono text-xs min-h-[88px]" value={config.key_pem || ''}
                        onChange={e => setCfg('key_pem', e.target.value)}
                        placeholder={config.key_configured ? '已配置 · 留空保留原私钥' : '-----BEGIN PRIVATE KEY-----'} />
                      <input type="file" accept=".pem,.key,.txt" className="mt-1 text-xs"
                        onChange={e => readPemFile(e.target.files?.[0], 'key_pem')} />
                    </div>
                    <div>
                      <label className="fl block mb-1">ALPN（可选）</label>
                      <input className="input-field font-mono" value={config.alpn || ''}
                        onChange={e => setCfg('alpn', e.target.value)} placeholder="一般留空" />
                    </div>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={!!config.allow_insecure}
                        onChange={e => setCfg('allow_insecure', e.target.checked)} />
                      <span>客户端 allow insecure（分享 URI insecure=1，自签时勾选）</span>
                    </label>
                  </FormSection>
                  <FormSection title="监听与高级" collapsible
                    defaultOpen={config.sniffing === false || !!config.tcp_fast_open || config.ntp === false}>
                    <Select value={config.listen || '::'} onChange={v => setCfg('listen', v)}
                      options={[
                        { value: '::', label: '::（双栈）' },
                        { value: '0.0.0.0', label: '0.0.0.0（仅 IPv4）' },
                      ]} />
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={config.ntp !== false} onChange={e => setCfg('ntp', e.target.checked)} />
                      <span>NTP</span>
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={config.sniffing !== false} onChange={e => setCfg('sniffing', e.target.checked)} />
                      <span className="font-mono">sniff</span>
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={!!config.tcp_fast_open} onChange={e => setCfg('tcp_fast_open', e.target.checked)} />
                      <span className="font-mono">tcp_fast_open</span>
                    </label>
                  </FormSection>
                </div>
              )}

              {protocol === 'naive' && (
                <div className="border-t border-line pt-4 space-y-3">
                  <div className="rounded-[12px] border border-amber-200 bg-amber-50/80 dark:bg-amber-950/30 dark:border-amber-800 px-4 py-3 text-[12.5px] text-amber-900 dark:text-amber-100 leading-relaxed">
                    本面板使用 <strong>sing-box naive inbound</strong>（协议兼容）。原版服务端为 Caddy + forwardproxy / HAProxy 前端，一期不部署 Caddy 栈。
                  </div>
                  <FormSection title="访问与密钥" hint="Basic Auth 用户名密码。">
                    <div className="grid sm:grid-cols-2 gap-3">
                      <div>
                        <label className="fl block mb-1">用户名</label>
                        <input className="input-field font-mono" value={config.username || ''}
                          onChange={e => setCfg('username', e.target.value)}
                          placeholder="留空则自动生成" />
                      </div>
                      <div>
                        <label className="fl block mb-1">密码</label>
                        <div className="flex gap-2">
                          <input className="input-field font-mono text-xs flex-1" value={config.password || ''}
                            onChange={e => setCfg('password', e.target.value)}
                            placeholder={config.password_configured ? '已配置 · 留空保留' : '留空则自动生成'} />
                          <button type="button" className="btn-secondary text-sm shrink-0"
                            onClick={() => {
                              const u = config.username || (`u${Math.random().toString(16).slice(2, 6)}`)
                              const p = Array.from(crypto.getRandomValues(new Uint8Array(12)))
                                .map(b => b.toString(16).padStart(2, '0')).join('')
                              setConfig(c => ({ ...c, username: u, password: p }))
                              toast('已生成账密')
                            }}>生成</button>
                        </div>
                      </div>
                    </div>
                    <div>
                      <label className="fl block mb-1">分享主机 share_host（可选）</label>
                      <input className="input-field font-mono" value={config.share_host || ''}
                        onChange={e => setCfg('share_host', e.target.value)} />
                    </div>
                  </FormSection>
                  <FormSection title="域名与证书" hint="TLS 必填。推荐 ACME 一键；调试可用自签。">
                    <div>
                      <label className="fl block mb-1">域名（SNI）*</label>
                      <input className="input-field font-mono" value={config.server_name || ''}
                        onChange={e => {
                          const v = e.target.value
                          setConfig(c => ({
                            ...c,
                            server_name: v,
                            share_host: (c.share_host || '').trim() ? c.share_host : v,
                          }))
                        }} placeholder="vpn.example.com" />
                    </div>
                    {(config.cert_configured || config.key_configured || config.acme_enabled) && !(config.cert_pem || '').trim() && (
                      <div className="rounded-lg border border-line bg-raised/50 px-3 py-2 text-[12.5px]">
                        已保存证书{config.acme_enabled ? ' · ACME 自动续期' : ''}（脱敏）
                        {config.acme_not_after ? ` · 至 ${config.acme_not_after}` : ''}
                      </div>
                    )}
                    <div className="flex flex-wrap items-center gap-2">
                      <button type="button" className="btn-primary text-sm" disabled={acmeBusy} onClick={issueACME}>
                        {acmeBusy ? 'ACME 申请中…' : (config.acme_enabled || config.cert_configured ? '续期 / 重新申请 ACME' : '申请 Let\'s Encrypt（DNS-01）')}
                      </button>
                      <button type="button" className="btn-secondary text-sm" onClick={() => genKeys('selfsigned')}>
                        生成自签证书（调试）
                      </button>
                      <label className="inline-flex items-center gap-1.5 text-[12px] text-ink-soft">
                        <input type="checkbox" checked={acmeStaging} onChange={e => setAcmeStaging(e.target.checked)} />
                        Staging
                      </label>
                    </div>
                    <div>
                      <label className="fl block mb-1">证书 PEM</label>
                      <textarea className="input-field font-mono text-xs min-h-[88px]" value={config.cert_pem || ''}
                        onChange={e => setCfg('cert_pem', e.target.value)}
                        placeholder={config.cert_configured ? '已配置 · 留空保留' : '-----BEGIN CERTIFICATE-----'} />
                      <input type="file" accept=".pem,.crt,.cer,.txt" className="mt-1 text-xs"
                        onChange={e => readPemFile(e.target.files?.[0], 'cert_pem')} />
                    </div>
                    <div>
                      <label className="fl block mb-1">私钥 PEM</label>
                      <textarea className="input-field font-mono text-xs min-h-[88px]" value={config.key_pem || ''}
                        onChange={e => setCfg('key_pem', e.target.value)}
                        placeholder={config.key_configured ? '已配置 · 留空保留' : '-----BEGIN PRIVATE KEY-----'} />
                      <input type="file" accept=".pem,.key,.txt" className="mt-1 text-xs"
                        onChange={e => readPemFile(e.target.files?.[0], 'key_pem')} />
                    </div>
                  </FormSection>
                  <FormSection title="传输" hint="tcp ≈ https 客户端；udp ≈ quic；留空双栈。">
                    <Select
                      value={config.network || 'tcp'}
                      onChange={v => setCfg('network', v)}
                      options={[
                        { value: 'tcp', label: 'tcp（HTTPS / HTTP2）' },
                        { value: 'udp', label: 'udp（QUIC / HTTP3）' },
                        { value: '', label: '双栈（tcp+udp）' },
                      ]}
                    />
                  </FormSection>
                  <FormSection title="高级" collapsible defaultOpen={false}>
                    <div>
                      <label className="fl block mb-1">quic_congestion_control（可选）</label>
                      <input className="input-field font-mono" value={config.quic_congestion_control || ''}
                        onChange={e => setCfg('quic_congestion_control', e.target.value)}
                        placeholder="bbr（默认）" />
                    </div>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={!!config.allow_insecure}
                        onChange={e => setCfg('allow_insecure', e.target.checked)} />
                      <span>客户端 insecure</span>
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={config.ntp !== false} onChange={e => setCfg('ntp', e.target.checked)} />
                      <span>NTP</span>
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={config.sniffing !== false} onChange={e => setCfg('sniffing', e.target.checked)} />
                      <span className="font-mono">sniff</span>
                    </label>
                  </FormSection>
                </div>
              )}

              <label className="inline-flex items-center gap-2 text-sm">
                <input type="checkbox" checked={subVisible} onChange={e => setSubVisible(e.target.checked)} />
                订阅可见性
              </label>
            </div>
          )}

          {step === 2 && (
            <div>
              <h3 className="text-sm font-bold mb-2">选择节点</h3>
              <p className="text-[12.5px] text-ink-mut mb-3">
                仅显示线路节点。具备核心 <span className="font-mono">{core}</span> 的优先；未检测时发布会尝试从面板「代理核心缓存」自动推送。
              </p>
              <div className="border border-line rounded-xl overflow-hidden">
                <table className="tbl">
                  <thead>
                    <tr>
                      <th className="w-10"></th>
                      <th>节点</th>
                      <th>在线</th>
                      <th>核心</th>
                    </tr>
                  </thead>
                  <tbody>
                    {eligibleNodes.length === 0 ? (
                      <tr><td colSpan={4} className="text-center text-ink-mut py-8">暂无节点，请先在「线路节点」中添加线路</td></tr>
                    ) : eligibleNodes.map(n => {
                      const cores = nodeCores[n.id] || []
                      const has = coreMatches(cores, core)
                      return (
                        <tr key={n.id} className="cursor-pointer" onClick={() => toggleNode(n.id)}>
                          <td><input type="checkbox" checked={selected.has(n.id)} onChange={() => toggleNode(n.id)} onClick={e => e.stopPropagation()} /></td>
                          <td className="font-semibold">{n.name}</td>
                          <td>{n.online ? <Badge color="green">在线</Badge> : <Badge color="gray">离线</Badge>}</td>
                          <td className="text-[12px]">
                            {has ? <Badge color="green">{core}</Badge> : <span className="text-ink-mut">未检测（将尝试推送缓存）</span>}
                            {cores.length > 0 && (
                              <span className="text-ink-mut ml-2 font-mono">{cores.map(c => c.name).join(', ')}</span>
                            )}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {step === 3 && (
            <div>
              <h3 className="text-sm font-bold mb-2">确认发布</h3>
              <ul className="text-sm text-ink-soft space-y-1.5 mb-4">
                <li>服务：<strong>{name}</strong></li>
                <li>协议 / 核心：{protocol} / {core}</li>
                <li>端口：{config.listen_port || 443}</li>
                <li>节点数：{selected.size}</li>
              </ul>
              <p className="text-[12.5px] text-ink-mut mb-4">
                发布后 agent 会在节点启动核心：VLESS→xray；SS / SOCKS5 / AnyTLS / Naive→sing-box；mieru→mita。请放行监听端口。AnyTLS/Naive 需证书与域名；Naive 为 sing-box 协议兼容（非 Caddy 原版）。
              </p>
            </div>
          )}

          {step === 4 && (
            <div>
              <h3 className="text-sm font-bold mb-3">完成</h3>
              {result?.results && (
                <div className="border border-line rounded-xl overflow-hidden mb-4">
                  <table className="tbl">
                    <thead>
                      <tr>
                        <th>节点 ID</th>
                        <th>结果</th>
                        <th>URI</th>
                      </tr>
                    </thead>
                    <tbody>
                      {result.results.map((r, i) => (
                        <tr key={i}>
                          <td className="font-mono">{r.node_id}</td>
                          <td>
                            {r.ok
                              ? <Badge color="green">{r.dry_run ? '就绪（未启动进程）' : '就绪'}</Badge>
                              : <Badge color="red">失败</Badge>}
                            {r.warning && <div className="text-[11px] text-amber-600 mt-0.5">{r.warning}</div>}
                            {r.error && <div className="text-[11px] text-rose-600 mt-0.5">{r.error}</div>}
                          </td>
                          <td className="max-w-[320px]">
                            {r.uri ? (
                              <button type="button" className="text-left text-[12px] font-mono text-emerald-600 hover:underline truncate block max-w-full"
                                onClick={() => { copyToClipboard(r.uri); toast('已复制') }}>
                                {r.uri}
                              </button>
                            ) : '—'}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              <div className="flex flex-wrap gap-2">
                <button type="button" className="btn-primary" onClick={syncRepo}>同步到落地仓库</button>
                <button type="button" className="btn-secondary" onClick={() => navigate(`/proxy-services/${serviceId}`)}>查看服务</button>
                <button type="button" className="btn-secondary" onClick={() => navigate('/proxy-services')}>返回列表</button>
              </div>
            </div>
          )}
          </div>

          {/* Sticky step actions — always visible; form body scrolls above */}
          {step === 0 && (
            <div className="shrink-0 border-t border-line px-6 py-3 bg-surface flex justify-end gap-2">
              <span className="text-[12px] text-ink-mut mr-auto self-center">选择协议模板后进入配置</span>
            </div>
          )}
          {step === 1 && (
            <div className="shrink-0 border-t border-line px-6 py-3 bg-surface flex justify-end gap-2">
              <button type="button" className="btn-secondary" onClick={() => setStep(0)}>上一步</button>
              <button type="button" className="btn-primary" onClick={async () => {
                if (await saveConfig()) setStep(2)
              }}>下一步</button>
            </div>
          )}
          {step === 2 && (
            <div className="shrink-0 border-t border-line px-6 py-3 bg-surface flex justify-end gap-2">
              <button type="button" className="btn-secondary" onClick={() => setStep(1)}>上一步</button>
              <button type="button" className="btn-primary" disabled={selected.size === 0} onClick={() => setStep(3)}>
                下一步（已选 {selected.size}）
              </button>
            </div>
          )}
          {step === 3 && (
            <div className="shrink-0 border-t border-line px-6 py-3 bg-surface flex justify-end gap-2">
              <button type="button" className="btn-secondary" onClick={() => setStep(2)}>上一步</button>
              <button type="button" className="btn-primary" disabled={publishing} onClick={publish}>
                {publishing ? '发布中…' : '发布'}
              </button>
            </div>
          )}
        </Panel>
      </div>
    </Layout>
  )
}
