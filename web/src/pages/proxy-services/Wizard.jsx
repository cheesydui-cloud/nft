import { useState, useEffect, useMemo } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api } from '../../lib/api'
import { Layout, useToast } from '../../components/Layout'
import { Loading, Badge, Select } from '../../components/ui'
import { PageHeader, Panel } from '../../components/page'
import { copyToClipboard } from '../../lib/clipboard'
import { REALITY_DOMAIN_POOL, REALITY_FP_OPTIONS, REALITY_NETWORK_OPTIONS } from '../../lib/realityDomains'

const STEPS = ['选协议模板', '协议配置', '选择节点', '发布', '完成']

const TEMPLATES = [
  { protocol: 'vless', core: 'xray', title: 'VLESS', desc: '免证书 REALITY，抗封锁默认推荐' },
  { protocol: 'shadowsocks', core: 'sing-box', title: 'Shadowsocks', desc: '轻量、客户端生态最广' },
  { protocol: 'mieru', core: 'mieru', title: 'mieru', desc: '多路复用抗探测，TCP/UDP 双传输' },
]

const SS_METHODS = [
  '2022-blake3-aes-128-gcm',
  '2022-blake3-aes-256-gcm',
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
  path: '/',
  host: '',
  spider_x: '',
  xhttp_mode: 'auto',
})

const emptySS = () => ({
  listen_port: 443,
  share_host: '',
  method: '2022-blake3-aes-128-gcm',
  password: '',
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
        setConfig(cfg)
      } catch { /* keep */ }
      setStep(1)
    }).catch(err => toast(err.message, 'error')).finally(() => setLoading(false))
  }, [editId])

  const pickTemplate = (t) => {
    setProtocol(t.protocol)
    setCore(t.core)
    if (t.protocol === 'vless') setConfig(emptyVless())
    else if (t.protocol === 'shadowsocks') setConfig(emptySS())
    else setConfig(emptyMieru())
    setStep(1)
  }

  const setCfg = (key, val) => setConfig(c => ({ ...c, [key]: val }))

  const [probeNodeId, setProbeNodeId] = useState('')
  const [probingDest, setProbingDest] = useState(false)
  const [destProbe, setDestProbe] = useState(null)
  const [genEncBusy, setGenEncBusy] = useState(false)

  const onlineNodes = useMemo(
    () => nodes.filter(n => n.online === 1 || n.online === true),
    [nodes],
  )

  const genKeys = async (kind) => {
    try {
      if (kind === 'vlessenc') setGenEncBusy(true)
      const d = await api.get(`/proxy-services/gen-keys?kind=${kind || 'reality'}`)
      if (kind === 'short_id') {
        setCfg('short_id', d.short_id)
        toast('已生成 short_id')
      } else if (kind === 'vlessenc') {
        setCfg('encryption', d.encryption || '')
        setCfg('decryption', d.decryption || '')
        toast(d.xray_version ? `已生成 vlessenc（xray ${d.xray_version}）` : '已生成 vlessenc')
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
    if (protocol === 'vless' && !(config.server_name || '').trim()) {
      toast('请填写 server-name（REALITY 回源 SNI），可从域名池选择', 'error')
      return false
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
      <div className="h-full flex flex-col max-w-5xl">
        <PageHeader title={isEdit ? '编辑代理服务' : '发布服务'}
          actions={<button type="button" className="btn-secondary" onClick={() => navigate('/proxy-services')}>返回列表</button>}
        />

        {/* Steps */}
        <div className="flex flex-wrap gap-2 mb-5">
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

        <Panel>
          {/* Panel uses overflow-hidden + rounded corners; without padding the
              first label/control is clipped at the card edge (looks like「协议」→「议」). */}
          <div className="px-6 py-5">
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
                    if (v === 'vless') setConfig(emptyVless())
                    else if (v === 'shadowsocks') setConfig(emptySS())
                    else setConfig(emptyMieru())
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

              {protocol === 'vless' && (
                <div className="space-y-3 border-t border-line pt-4">
                  <div className="grid sm:grid-cols-2 gap-3">
                    <div>
                      <label className="fl block mb-1">server-name（回源 / REALITY SNI）</label>
                      <input className="input-field font-mono" value={config.server_name || ''} onChange={e => setCfg('server_name', e.target.value)} placeholder="cdn.example.com" />
                      <p className="text-[11px] text-ink-mut mt-1">回源站 / REALITY SNI / TLS SNI</p>
                    </div>
                    <div>
                      <label className="fl block mb-1">域名池</label>
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
                      <p className="text-[11px] text-ink-mut mt-1">按节点所在地区挑选；一个 inbound 只有一个 dest。上线前请在该节点实测 TLS1.3 + h2 + X25519</p>
                    </div>
                    <div>
                      <label className="fl block mb-1">server-port</label>
                      <input className="input-field font-mono" type="number" value={config.server_port || 443} onChange={e => setCfg('server_port', Number(e.target.value) || 443)} />
                      <p className="text-[11px] text-ink-mut mt-1">回源端口，默认 443</p>
                    </div>
                    <div>
                      <label className="fl block mb-1">指纹</label>
                      <Select value={config.fingerprint || 'chrome'} onChange={v => setCfg('fingerprint', v)}
                        options={REALITY_FP_OPTIONS.map(v => ({ value: v, label: v }))} />
                      <p className="text-[11px] text-ink-mut mt-1">客户端 ClientHello 伪装；默认 chrome 即可</p>
                    </div>
                    <div>
                      <label className="fl block mb-1">max_time_difference</label>
                      <input className="input-field font-mono" type="number" value={config.max_time_difference ?? 60000}
                        onChange={e => setCfg('max_time_difference', Number(e.target.value))} />
                      <p className="text-[11px] text-ink-mut mt-1">毫秒整数，如 60000=1m，可空</p>
                    </div>
                    <div>
                      <label className="fl block mb-1">传输层</label>
                      <Select value={config.network || 'tcp'} onChange={v => {
                        setCfg('network', v)
                        // vision only valid on tcp
                        if (v !== 'tcp' && config.flow === 'xtls-rprx-vision') setCfg('flow', 'none')
                        if (v === 'tcp' && (!config.flow || config.flow === 'none')) setCfg('flow', 'xtls-rprx-vision')
                      }} options={REALITY_NETWORK_OPTIONS} />
                      <p className="text-[11px] text-ink-mut mt-1">抗封锁默认 tcp；ws/xhttp 会多一层 HTTP 特征，仅在需要时选用</p>
                    </div>
                    <div>
                      <label className="fl block mb-1">flow</label>
                      <Select value={config.flow === '' || config.flow === 'none' ? 'none' : (config.flow || 'xtls-rprx-vision')}
                        onChange={v => setCfg('flow', v === 'none' ? 'none' : v)}
                        options={[
                          { value: 'xtls-rprx-vision', label: 'xtls-rprx-vision' },
                          { value: 'none', label: '关' },
                        ]} />
                      <p className="text-[11px] text-ink-mut mt-1">仅 tcp 推荐 vision；其它传输层自动关闭</p>
                    </div>
                    <div>
                      <label className="fl block mb-1">decryption（服务端）</label>
                      <input className="input-field font-mono text-xs" value={config.decryption || ''} onChange={e => setCfg('decryption', e.target.value)}
                        placeholder="none 或 mlkem…（高级）" />
                    </div>
                    <div className="sm:col-span-2">
                      <label className="fl block mb-1">encryption（客户端）</label>
                      <div className="flex flex-wrap gap-2 items-center">
                        <input className="input-field font-mono text-xs flex-1 min-w-[200px]" value={config.encryption || ''} onChange={e => setCfg('encryption', e.target.value)}
                          placeholder="留空 = none；可填 xray vlessenc 输出" />
                        <button type="button" className="btn-primary text-sm" disabled={genEncBusy}
                          onClick={() => genKeys('vlessenc')}>{genEncBusy ? '生成中…' : '生成 vlessenc'}</button>
                        <button type="button" className="btn-secondary text-sm" onClick={() => {
                          setCfg('encryption', '')
                          setCfg('decryption', '')
                          toast('已清空 encryption / decryption（使用 none）')
                        }}>清空</button>
                      </div>
                      <p className="text-[11px] text-ink-mut mt-1">
                        可选 ML-KEM / vlessenc：一键调用面板缓存的 xray 生成密钥对，写入 decryption（服务端）与 encryption（客户端）。需先在「代理核心缓存」拉取支持 vlessenc 的 xray。留空即 none。
                      </p>
                    </div>
                  </div>

                  <div className="border border-dashed border-line rounded-xl p-4 space-y-3">
                    <div className="text-sm font-bold">REALITY dest 探测</div>
                    <p className="text-[12px] text-ink-mut m-0">
                      从线路节点侧对 <span className="font-mono">server-name:server-port</span> 做 TLS 握手，检查 TLS1.3 / h2 / 证书与 SNI。比面板本机探测更接近真实回源路径。
                    </p>
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
                      <div className="flex flex-wrap gap-2">
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

                  {(config.network === 'ws' || config.network === 'httpupgrade' || config.network === 'xhttp') && (
                    <div className="border border-dashed border-line rounded-xl p-4 space-y-3">
                      <div className="text-sm font-bold">传输层参数 · {config.network}</div>
                      <div className="grid sm:grid-cols-2 gap-3">
                        <div>
                          <label className="fl block mb-1">path</label>
                          <input className="input-field font-mono" value={config.path || '/'} onChange={e => setCfg('path', e.target.value || '/')} />
                        </div>
                        <div>
                          <label className="fl block mb-1">host（可选）</label>
                          <input className="input-field font-mono" value={config.host || ''} onChange={e => setCfg('host', e.target.value)} placeholder="默认与 SNI 一致时可留空" />
                        </div>
                        {config.network === 'xhttp' && (
                          <div>
                            <label className="fl block mb-1">xhttp mode</label>
                            <Select value={config.xhttp_mode || 'auto'} onChange={v => setCfg('xhttp_mode', v)}
                              options={['auto', 'packet-up', 'stream-up', 'stream-one'].map(v => ({ value: v, label: v }))} />
                          </div>
                        )}
                      </div>
                    </div>
                  )}

                  <div className="border border-dashed border-line rounded-xl p-4 space-y-3">
                    <div className="text-sm font-bold">安全层 · REALITY</div>
                    <div className="grid sm:grid-cols-2 gap-3">
                      <div>
                        <label className="fl block mb-1">private_key</label>
                        <input className="input-field font-mono text-xs" value={config.private_key || ''} onChange={e => setCfg('private_key', e.target.value)} />
                      </div>
                      <div>
                        <label className="fl block mb-1">public_key</label>
                        <input className="input-field font-mono text-xs" value={config.public_key || ''} onChange={e => setCfg('public_key', e.target.value)} />
                      </div>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <button type="button" className="btn-primary text-sm" onClick={() => genKeys('reality')}>生成密钥</button>
                      <button type="button" className="btn-secondary text-sm" onClick={() => genKeys('short_id')}>生成 short_id</button>
                    </div>
                    <div className="grid sm:grid-cols-2 gap-3">
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
                    <p className="text-[11px] text-ink-mut m-0">
                      密钥始终成对生成/覆盖。部署默认仅 REALITY；shortId 默认只接受已配置值。
                    </p>
                  </div>
                </div>
              )}

              {protocol === 'shadowsocks' && (
                <div className="border-t border-line pt-4 space-y-3">
                  <div>
                    <label className="fl block mb-1">method</label>
                    <Select value={config.method || SS_METHODS[0]} onChange={v => setCfg('method', v)}
                      options={SS_METHODS.map(m => ({ value: m, label: m }))} />
                  </div>
                  <p className="text-[12px] text-ink-mut">密码发布时自动生成（SS2022 密钥材料）。</p>
                </div>
              )}

              {protocol === 'mieru' && (
                <div className="border-t border-line pt-4 space-y-3">
                  <div>
                    <label className="fl block mb-1">传输层</label>
                    <div className="flex gap-3">
                      {['TCP', 'UDP'].map(t => {
                        const on = (config.transports || []).includes(t)
                        return (
                          <label key={t} className="inline-flex items-center gap-2 text-sm">
                            <input type="checkbox" checked={on} onChange={() => {
                              const cur = new Set(config.transports || [])
                              if (cur.has(t)) cur.delete(t)
                              else cur.add(t)
                              setCfg('transports', [...cur])
                            }} />
                            {t}
                          </label>
                        )
                      })}
                    </div>
                  </div>
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
                </div>
              )}

              <label className="inline-flex items-center gap-2 text-sm">
                <input type="checkbox" checked={subVisible} onChange={e => setSubVisible(e.target.checked)} />
                订阅可见性
              </label>

              <div className="flex justify-end gap-2 pt-2">
                <button type="button" className="btn-secondary" onClick={() => setStep(0)}>上一步</button>
                <button type="button" className="btn-primary" onClick={async () => {
                  if (await saveConfig()) setStep(2)
                }}>下一步</button>
              </div>
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
              <div className="flex justify-end gap-2 mt-4">
                <button type="button" className="btn-secondary" onClick={() => setStep(1)}>上一步</button>
                <button type="button" className="btn-primary" disabled={selected.size === 0} onClick={() => setStep(3)}>
                  下一步（已选 {selected.size}）
                </button>
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
                发布后 agent 会在节点启动核心：VLESS 需 xray、Shadowsocks 需 sing-box、mieru 需 mita（服务端）。请放行监听端口；VLESS 务必填写 server-name（REALITY 回源）。
              </p>
              <div className="flex justify-end gap-2">
                <button type="button" className="btn-secondary" onClick={() => setStep(2)}>上一步</button>
                <button type="button" className="btn-primary" disabled={publishing} onClick={publish}>
                  {publishing ? '发布中…' : '发布'}
                </button>
              </div>
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
        </Panel>
      </div>
    </Layout>
  )
}
