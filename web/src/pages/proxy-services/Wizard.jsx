import { useState, useEffect, useMemo } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api } from '../../lib/api'
import { Layout, useToast } from '../../components/Layout'
import { Loading, Badge, Select } from '../../components/ui'
import { PageHeader, Panel } from '../../components/page'
import { copyToClipboard } from '../../lib/clipboard'

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
  encryption: '',
  decryption: '',
  uuid: '',
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

  const genKeys = async (kind) => {
    try {
      const d = await api.get(`/proxy-services/gen-keys?kind=${kind || 'reality'}`)
      if (kind === 'short_id') setCfg('short_id', d.short_id)
      else {
        setCfg('private_key', d.private_key)
        setCfg('public_key', d.public_key)
      }
      toast('已生成')
    } catch (err) { toast(err.message, 'error') }
  }

  const saveConfig = async () => {
    if (!name.trim()) { toast('请填写名称', 'error'); return false }
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
                    </div>
                    <div>
                      <label className="fl block mb-1">server-port</label>
                      <input className="input-field font-mono" type="number" value={config.server_port || 443} onChange={e => setCfg('server_port', Number(e.target.value) || 443)} />
                    </div>
                    <div>
                      <label className="fl block mb-1">指纹</label>
                      <Select value={config.fingerprint || 'chrome'} onChange={v => setCfg('fingerprint', v)}
                        options={['chrome', 'firefox', 'safari', 'ios', 'android', 'edge', 'random'].map(v => ({ value: v, label: v }))} />
                    </div>
                    <div>
                      <label className="fl block mb-1">传输层</label>
                      <Select value={config.network || 'tcp'} onChange={v => setCfg('network', v)}
                        options={[{ value: 'tcp', label: 'tcp（裸 TCP）' }]} />
                    </div>
                    <div>
                      <label className="fl block mb-1">flow</label>
                      <Select value={config.flow || 'xtls-rprx-vision'} onChange={v => setCfg('flow', v)}
                        options={[{ value: 'xtls-rprx-vision', label: 'xtls-rprx-vision' }, { value: '', label: '（空）' }]} />
                    </div>
                    <div>
                      <label className="fl block mb-1">max_time_difference</label>
                      <input className="input-field font-mono" type="number" value={config.max_time_difference ?? 60000}
                        onChange={e => setCfg('max_time_difference', Number(e.target.value))} />
                    </div>
                  </div>
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
                    <div>
                      <label className="fl block mb-1">short_id</label>
                      <input className="input-field font-mono" value={config.short_id || ''} onChange={e => setCfg('short_id', e.target.value)} />
                    </div>
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
