import { useState, useEffect } from 'react'
import { api } from '../lib/api'
import { Layout, useToast, useUser } from '../components/Layout'
import { Loading, useConfirm } from '../components/ui'

export default function Settings() {
  const [form, setForm] = useState({
    panel_url: '',
    panel_name: '',
    show_rate_to_user: false,
    pool_size: 4,
    cf_token_configured: false,
    cf_token_prefix: '',
    cf_api_token: '',
    cf_clear_token: false,
    cf_zone_name: '',
    cf_ttl: 1,
    komari_url: '',
  })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const toast = useToast()
  const confirm = useConfirm()
  const { version, refreshUser } = useUser()

  // panel self-update
  const [upd, setUpd] = useState(null)
  const [updBusy, setUpdBusy] = useState(false)
  const [updMsg, setUpdMsg] = useState('')

  // migrate
  const [migURL, setMigURL] = useState('')
  const [migBusy, setMigBusy] = useState(false)
  const [migStatus, setMigStatus] = useState(null)
  const [migGuideOpen, setMigGuideOpen] = useState(true)

  // proxy core cache
  const [cores, setCores] = useState([])
  const [coreForm, setCoreForm] = useState({ type: 'xray', version: '', url: '', sha256: '', arch: '' })
  const [coreBusy, setCoreBusy] = useState(false)
  const [coreError, setCoreError] = useState('')

  const loadCores = () => {
    api.get('/proxy-cores').then(d => setCores(d.cores || [])).catch(() => {})
  }

  useEffect(() => {
    api.get('/settings').then(data => {
      setForm(f => ({
        ...f,
        panel_url: data.panel_url || '',
        panel_name: data.panel_name || '',
        show_rate_to_user: !!data.show_rate_to_user,
        pool_size: data.pool_size ?? 4,
        cf_token_configured: !!data.cf_token_configured,
        cf_token_prefix: data.cf_token_prefix || '',
        cf_api_token: '',
        cf_clear_token: false,
        cf_zone_name: data.cf_zone_name || '',
        cf_ttl: data.cf_ttl ?? 1,
        komari_url: data.komari_url || '',
      }))
      if (data.panel_url) setMigURL(data.panel_url)
    }).catch(e => setError(e.message)).finally(() => setLoading(false))
  }, [])

  const loadMigStatus = () => {
    api.get('/migrate/status').then(setMigStatus).catch(() => {})
  }
  useEffect(() => { loadMigStatus() }, [])
  useEffect(() => { loadCores() }, [])
  useEffect(() => {
    api.get('/system/update').then(setUpd).catch(() => {})
  }, [])

  const set = (k, v) => setForm(f => ({ ...f, [k]: v }))

  const submit = async (e) => {
    e.preventDefault()
    setError('')
    const ps = parseInt(form.pool_size, 10)
    if (isNaN(ps) || ps < 0 || ps > 64) {
      setError('TCP 连接池数必须在 0-64 之间')
      return
    }
    const ttl = parseInt(form.cf_ttl, 10)
    if (isNaN(ttl) || ttl < 1) {
      setError('CF TTL 至少为 1（1 = Auto）')
      return
    }
    setSaving(true)
    try {
      const body = {
        panel_url: form.panel_url,
        panel_name: form.panel_name,
        show_rate_to_user: form.show_rate_to_user,
        pool_size: ps,
        cf_zone_name: form.cf_zone_name,
        cf_ttl: ttl,
        cf_clear_token: !!form.cf_clear_token,
        komari_url: form.komari_url,
      }
      if (!form.cf_clear_token && form.cf_api_token.trim()) {
        body.cf_api_token = form.cf_api_token.trim()
      }
      await api.post('/settings', body)
      toast('设置已保存')
      const data = await api.get('/settings')
      setForm(f => ({
        ...f,
        cf_token_configured: !!data.cf_token_configured,
        cf_token_prefix: data.cf_token_prefix || '',
        cf_api_token: '',
        cf_clear_token: false,
        cf_zone_name: data.cf_zone_name || '',
        cf_ttl: data.cf_ttl ?? 1,
        komari_url: data.komari_url || '',
      }))
      refreshUser?.()
    } catch (err) { setError(err.message) } finally { setSaving(false) }
  }

  const checkPanelUpdate = async () => {
    setUpdBusy(true)
    setUpdMsg('')
    try {
      const d = await api.post('/system/update/check', {})
      setUpd(d)
      if (d.update_available) toast(`发现新版本 ${d.latest_version}`)
      else toast(d.latest_version ? `已是最新（${d.latest_version}）` : '检查完成')
      if (d.message) setUpdMsg(d.message)
    } catch (err) {
      toast(err.message, 'error')
      setUpdMsg(err.message)
    } finally {
      setUpdBusy(false)
    }
  }

  const applyPanelUpdate = async () => {
    if (!(await confirm({
      title: '更新面板',
      message: '将下载并替换本机 nft-server / nft-agent，并重启面板服务（约数十秒）。节点 agent 需在「线路节点」单独推送。确定继续？',
      confirmText: '立即更新',
    }))) return
    setUpdBusy(true)
    setUpdMsg('已发起升级，面板即将重启…')
    try {
      const target = upd?.latest_version && upd?.update_available ? upd.latest_version : undefined
      await api.post('/system/update', target ? { release: target } : {})
      toast('升级已启动，等待面板重启…')
      // Poll until version changes or timeout
      const before = version || upd?.current_version || ''
      const start = Date.now()
      const poll = setInterval(async () => {
        try {
          const me = await api.get('/me')
          const v = me?.version || ''
          if (v && before && v !== before) {
            clearInterval(poll)
            setUpdBusy(false)
            setUpdMsg('')
            toast(`升级完成：${v}`)
            refreshUser?.()
            api.get('/system/update').then(setUpd).catch(() => {})
          } else if (Date.now() - start > 180000) {
            clearInterval(poll)
            setUpdBusy(false)
            setUpdMsg('等待超时：请刷新页面或 SSH 查看 journalctl -u nft-server')
            toast('升级可能仍在进行，请刷新页面确认版本', 'error')
          }
        } catch {
          // panel restarting — keep waiting
        }
      }, 2500)
    } catch (err) {
      toast(err.message, 'error')
      setUpdMsg(err.message)
      setUpdBusy(false)
    }
  }

  const downloadExport = async () => {
    setMigBusy(true)
    try {
      const res = await fetch('/api/migrate/export', { credentials: 'same-origin' })
      if (!res.ok) {
        let msg = `导出失败（${res.status}）`
        try {
          const j = await res.json()
          if (j?.error) msg = j.error
        } catch { /* ignore */ }
        throw new Error(msg)
      }
      const blob = await res.blob()
      const cd = res.headers.get('Content-Disposition') || ''
      let name = 'panel-migrate.db'
      const m = /filename="([^"]+)"/.exec(cd)
      if (m) name = m[1]
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      a.download = name
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(a.href)
      toast('数据库已导出：' + name)
    } catch (err) { toast(err.message, 'error') }
    finally { setMigBusy(false) }
  }

  const pushRedirect = async () => {
    const url = migURL.trim()
    if (!url) { toast('请填写新面板地址', 'error'); return }
    if (!(await confirm({
      title: '通知 Agent 切换面板',
      message: `将把所有在线 agent 的连接地址切换到：\n${url}\n\n请确认：\n1) 新面板已导入本机导出的数据库\n2) 新面板已能登录\n3) 本机（旧面板）暂时保持运行以便推送与补推\n\n切换后在线节点会断开并连向新地址；Token 随库迁移，无需重置。`,
      confirmText: '确认推送',
      danger: true,
    }))) return
    setMigBusy(true)
    try {
      const res = await api.post('/migrate/redirect', { panel_url: url })
      toast(res?.message || `已推送 ${res?.pushed || 0} 个在线节点`)
      if (res?.panel_url) setForm(f => ({ ...f, panel_url: res.panel_url }))
      loadMigStatus()
    } catch (err) { toast(err.message, 'error') }
    finally { setMigBusy(false) }
  }

  const clearPending = async () => {
    if (!(await confirm({
      title: '清除待迁移',
      message: '清除后，晚连上的节点将不再被自动推送到新地址。请确认节点已全部迁到新面板。',
      confirmText: '清除',
    }))) return
    setMigBusy(true)
    try {
      await api.post('/migrate/clear-pending', {})
      toast('已清除待迁移地址')
      loadMigStatus()
    } catch (err) { toast(err.message, 'error') }
    finally { setMigBusy(false) }
  }


  const setCore = (k, v) => setCoreForm(f => ({ ...f, [k]: v }))

  const fetchCores = async () => {
    setCoreError('')
    setCoreBusy(true)
    try {
      const body = {
        type: coreForm.type,
        version: coreForm.version.trim() || undefined,
        url: coreForm.url.trim() || undefined,
        sha256: coreForm.sha256.trim() || undefined,
      }
      if (coreForm.arch) body.arch = coreForm.arch
      if (coreForm.url.trim() && !coreForm.arch) {
        setCoreError('自定义 URL 时请选择架构（amd64 或 arm64）')
        setCoreBusy(false)
        return
      }
      const res = await api.post('/proxy-cores/fetch', body)
      setCores(res.cores || [])
      const fails = (res.results || []).filter(r => !r.ok)
      if (fails.length) {
        toast(`部分架构失败：${fails.map(f => f.arch + ': ' + (f.error || '')).join('；')}`)
      } else {
        toast('核心缓存已更新')
      }
    } catch (err) {
      setCoreError(err.message)
    } finally {
      setCoreBusy(false)
    }
  }

  const deleteCore = async (type, arch) => {
    if (!(await confirm({ title: '删除缓存', message: `确定删除 ${type}/${arch} 的核心缓存？` }))) return
    setCoreBusy(true)
    try {
      const res = await api.del(`/proxy-cores/${encodeURIComponent(type)}/${encodeURIComponent(arch)}`)
      setCores(res.cores || [])
      toast('已删除')
    } catch (err) {
      setCoreError(err.message)
    } finally {
      setCoreBusy(false)
    }
  }

  const formatSize = (n) => {
    if (!n) return '—'
    if (n < 1024) return n + ' B'
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB'
    return (n / 1024 / 1024).toFixed(1) + ' MB'
  }

  if (loading) return <Layout><Loading /></Layout>

  const pending = migStatus?.pending_panel_redirect_url || ''

  return (
    <Layout>
      <h1 className="m-0 text-2xl font-bold text-ink mb-[22px]">系统设置</h1>
      <div className="card" style={{ maxWidth: 980 }}>
        <div className="card-header"><h3 className="text-[16px] font-bold">面板信息</h3></div>
        <div className="px-6 py-[26px]">
          {error && <div className="mb-4 px-3 py-2 bg-red-50 border border-red-200 rounded text-red-600 text-sm">{error}</div>}
          <form onSubmit={submit}>
            <div className="flex items-center gap-6 mb-[22px]">
              <label className="w-[110px] flex-shrink-0 text-[14px] text-ink-soft">面板地址</label>
              <div className="flex-1 max-w-[560px]">
                <input className="input-field w-full" type="text" placeholder="http://1.2.3.4:7788 或 https://panel.example.com" value={form.panel_url} onChange={e => set('panel_url', e.target.value)} />
                <p className="text-[12px] text-ink-mut mt-1.5 m-0">节点升级会从该地址下载 agent。请带协议（http/https）；只写 IP:端口 时保存会自动补 http://。</p>
              </div>
            </div>
            <div className="flex items-center gap-6 pb-[22px] border-b border-line-soft">
              <label className="w-[110px] flex-shrink-0 text-[14px] text-ink-soft">面板名称</label>
              <input className="input-field max-w-[560px]" type="text" placeholder="nft" value={form.panel_name} onChange={e => set('panel_name', e.target.value)} />
            </div>

            <div className="pt-[22px]">
              <h3 className="text-[16px] font-bold text-ink mb-[22px]">转发设置</h3>
            </div>

            <div className="flex items-center gap-6 mb-[22px]">
              <label className="w-[110px] flex-shrink-0 text-[14px] text-ink-soft">显示倍率</label>
              <button type="button" role="switch" aria-checked={form.show_rate_to_user}
                className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${form.show_rate_to_user ? 'bg-emerald-600' : 'bg-gray-600'}`}
                onClick={() => set('show_rate_to_user', !form.show_rate_to_user)}>
                <span className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${form.show_rate_to_user ? 'translate-x-6' : 'translate-x-1'}`} />
              </button>
              <span className="text-[13px] text-ink-mut">向普通用户展示节点/链路倍率</span>
            </div>

            <div className="flex items-center gap-6 pb-[22px] border-b border-line-soft">
              <label className="w-[110px] flex-shrink-0 text-[14px] text-ink-soft">TCP 连接池</label>
              <input className="input-field w-[100px]" type="number" min="0" max="64" value={form.pool_size} onChange={e => set('pool_size', e.target.value)} />
              <span className="text-[13px] text-ink-mut">每端口预建立连接数（0 = 禁用，默认 4）</span>
            </div>

            <div className="pt-[22px]">
              <h3 className="text-[16px] font-bold text-ink mb-1">Cloudflare DNS</h3>
              <p className="text-[12px] text-ink-mut m-0 mb-[18px]">
                用于落地仓库与线路：目标填域名、保存时把「当前 IP」写入 CF 的 A 记录（仅 DNS / 灰云）。Token 只存服务端，接口不回显明文。
              </p>
            </div>

            <div className="flex items-start gap-6 mb-[22px]">
              <label className="w-[110px] flex-shrink-0 text-[14px] text-ink-soft pt-2">API Token</label>
              <div className="flex-1 max-w-[560px]">
                <input
                  className="input-field w-full font-mono text-sm"
                  type="password"
                  autoComplete="new-password"
                  placeholder={form.cf_token_configured ? `已配置（${form.cf_token_prefix}）· 留空不修改` : '粘贴 Zone.DNS Edit 权限的 Token'}
                  value={form.cf_api_token}
                  onChange={e => set('cf_api_token', e.target.value)}
                  disabled={form.cf_clear_token}
                />
                <div className="flex items-center gap-3 mt-2">
                  <label className="inline-flex items-center gap-1.5 text-[12px] text-ink-soft cursor-pointer">
                    <input type="checkbox" className="accent-emerald-600" checked={form.cf_clear_token}
                      onChange={e => set('cf_clear_token', e.target.checked)} />
                    清除已保存的 Token
                  </label>
                  {form.cf_token_configured && !form.cf_clear_token && (
                    <span className="text-[12px] text-emerald-600 font-semibold">已配置</span>
                  )}
                </div>
                <p className="text-[12px] text-ink-mut mt-1.5 m-0">权限：Zone → DNS → Edit，Zone → Zone → Read；作用域限定你的域名 Zone。</p>
              </div>
            </div>

            <div className="flex items-center gap-6 mb-[22px]">
              <label className="w-[110px] flex-shrink-0 text-[14px] text-ink-soft">默认 Zone</label>
              <div className="flex-1 max-w-[560px]">
                <input className="input-field w-full font-mono" type="text" placeholder="example.com"
                  value={form.cf_zone_name} onChange={e => set('cf_zone_name', e.target.value)} />
                <p className="text-[12px] text-ink-mut mt-1.5 m-0">落地/线路条目未单独填 Zone 时使用。须与 Token 有权限的域名一致。</p>
              </div>
            </div>

            <div className="flex items-center gap-6 pb-[22px] border-b border-line-soft">
              <label className="w-[110px] flex-shrink-0 text-[14px] text-ink-soft">TTL</label>
              <input className="input-field w-[100px]" type="number" min="1" value={form.cf_ttl}
                onChange={e => set('cf_ttl', e.target.value)} />
              <span className="text-[13px] text-ink-mut">秒；1 = Cloudflare Auto（推荐）</span>
            </div>

            <div className="pt-[22px]">
              <h3 className="text-[16px] font-bold text-ink mb-1">Komari 监控（并排）</h3>
              <p className="text-[12px] text-ink-mut m-0 mb-[18px]">
                独立部署 Komari 后填写访问地址；侧栏「监控」会出现「服务监控」外链（新标签打开）。不内嵌到本面板。
                例：Docker 映射 <code className="font-mono text-[11px]">25774</code>，再反代到子域名。
              </p>
            </div>
            <div className="flex items-center gap-6 pb-[22px] border-b border-line-soft">
              <label className="w-[110px] flex-shrink-0 text-[14px] text-ink-soft">Komari 地址</label>
              <div className="flex-1 max-w-[560px]">
                <input className="input-field w-full" type="text" placeholder="https://monitor.example.com"
                  value={form.komari_url} onChange={e => set('komari_url', e.target.value)} />
                <p className="text-[12px] text-ink-mut mt-1.5 m-0">留空则隐藏侧栏入口。探针 agent 需在各 VPS 单独安装到 Komari。</p>
              </div>
            </div>

            <div className="flex items-center gap-4 mt-[22px]">
              <button type="submit" disabled={saving} className="btn-primary">{saving ? '保存中…' : '保存设置'}</button>
            </div>
          </form>
        </div>
      </div>

      {/* 面板版本 / 自更新 */}
      <div className="card mt-5" style={{ maxWidth: 980 }}>
        <div className="card-header">
          <h3 className="text-[16px] font-bold m-0">面板版本</h3>
        </div>
        <div className="px-6 py-[22px] space-y-3">
          <p className="text-[13px] text-ink-soft m-0 leading-relaxed">
            从 GitHub 发布通道检查更新；升级会替换本机 <code className="font-mono text-[12px]">nft-server</code>、
            同步本地 agent 产物并重启服务。线路节点上的 agent 仍需在「线路节点」列表单独推送。
          </p>
          <div className="text-[14px] text-ink">
            当前：<span className="font-mono font-semibold">{upd?.current_version || version || '—'}</span>
            {upd?.arch_ok === false && <span className="text-amber-600 text-[12px] ml-2">（非 amd64，一键升级不可用）</span>}
          </div>
          <div className="text-[13px] text-ink-mut">
            通道：GitHub <span className="font-mono">{upd?.repo || 'cheesydui-cloud/nft'}</span> latest
            {upd?.gh_proxy ? <> · 代理 <span className="font-mono text-[12px]">{upd.gh_proxy}</span></> : null}
          </div>
          {upd?.update_available && (
            <div className="text-[14px] text-emerald-700 font-semibold">
              发现新版本 <span className="font-mono">{upd.latest_version}</span>
            </div>
          )}
          {!upd?.update_available && upd?.latest_version && (
            <div className="text-[13px] text-ink-mut">最新已发布：<span className="font-mono">{upd.latest_version}</span></div>
          )}
          {(updMsg || upd?.message) && (
            <div className="text-[12px] text-ink-mut bg-slate-50 border border-line rounded-md px-3 py-2">{updMsg || upd?.message}</div>
          )}
          <div className="flex flex-wrap items-center gap-3 pt-1">
            <button type="button" className="btn-primary" disabled={updBusy} onClick={checkPanelUpdate}>
              {updBusy ? '处理中…' : '检查更新'}
            </button>
            <button type="button" className="btn-secondary" disabled={updBusy || upd?.can_apply === false} onClick={applyPanelUpdate}
              title={upd?.can_apply === false ? (upd?.message || '当前环境无法一键升级') : '执行 nft-upgrade'}>
              立即更新
            </button>
          </div>
        </div>
      </div>

      {/* 代理核心缓存 */}
      <div className="card mt-5" style={{ maxWidth: 980 }}>
        <div className="card-header">
          <h3 className="text-[16px] font-bold m-0">代理核心缓存</h3>
        </div>
        <div className="px-6 py-[22px] space-y-5">
          <p className="text-[13px] text-ink-soft m-0 leading-relaxed">
            从 GitHub 官方 release 下载并缓存 <code className="font-mono text-[12px]">xray</code> /
            <code className="font-mono text-[12px]">sing-box</code> /
            <code className="font-mono text-[12px]">mita</code>（mieru 服务端）的 linux 二进制（amd64 / arm64）。
            发布代理服务时，若节点本机没有对应核心，面板会按节点架构自动推送缓存并安装到
            <code className="font-mono text-[12px]">/var/lib/nft/cores/…</code>。
            版本留空 = 官方 latest；面板机需能访问 GitHub（或使用安装时的 gh-proxy / 自定义 URL）。
          </p>

          {coreError && (
            <div className="text-[13px] text-red-600 bg-red-50 border border-red-200 rounded-md px-3 py-2">{coreError}</div>
          )}

          <div className="overflow-x-auto rounded-[10px] border border-line">
            <table className="tbl w-full text-[13px]">
              <thead>
                <tr>
                  <th className="text-left">类型</th>
                  <th className="text-left">架构</th>
                  <th className="text-left">版本</th>
                  <th className="text-left">大小</th>
                  <th className="text-left">SHA256</th>
                  <th className="text-left">操作</th>
                </tr>
              </thead>
              <tbody>
                {cores.length === 0 ? (
                  <tr><td colSpan={6} className="text-ink-mut">暂无缓存，请在下方下载</td></tr>
                ) : cores.map(c => (
                  <tr key={`${c.type}-${c.arch}`}>
                    <td className="font-semibold">{c.type}</td>
                    <td className="font-mono text-xs">{c.arch}</td>
                    <td className="font-mono text-xs">{c.version || '—'}</td>
                    <td className="text-xs">{formatSize(c.size)}</td>
                    <td className="font-mono text-[11px] text-ink-mut max-w-[160px] truncate" title={c.sha256}>{c.sha256 ? c.sha256.slice(0, 12) + '…' : '—'}</td>
                    <td>
                      <button type="button" className="text-red-600 text-[12px] font-semibold bg-transparent border-0 cursor-pointer p-0"
                        disabled={coreBusy} onClick={() => deleteCore(c.type, c.arch)}>删除</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <div className="flex items-center gap-3">
              <label className="w-[72px] flex-shrink-0 text-[13px] text-ink-soft">类型</label>
              <select className="input-field flex-1" value={coreForm.type} onChange={e => setCore('type', e.target.value)}>
                <option value="xray">xray（VLESS）</option>
                <option value="sing-box">sing-box（Shadowsocks）</option>
                <option value="mita">mita（mieru 服务端）</option>
              </select>
            </div>
            <div className="flex items-center gap-3">
              <label className="w-[72px] flex-shrink-0 text-[13px] text-ink-soft">架构</label>
              <select className="input-field flex-1" value={coreForm.arch} onChange={e => setCore('arch', e.target.value)}>
                <option value="">双架构（amd64 + arm64）</option>
                <option value="amd64">仅 amd64</option>
                <option value="arm64">仅 arm64</option>
              </select>
            </div>
            <div className="flex items-center gap-3">
              <label className="w-[72px] flex-shrink-0 text-[13px] text-ink-soft">版本</label>
              <input className="input-field flex-1 font-mono" placeholder="留空 = latest，如 v1.12.12"
                value={coreForm.version} onChange={e => setCore('version', e.target.value)} />
            </div>
            <div className="flex items-center gap-3">
              <label className="w-[72px] flex-shrink-0 text-[13px] text-ink-soft">SHA256</label>
              <input className="input-field flex-1 font-mono" placeholder="可选，校验解压后的二进制"
                value={coreForm.sha256} onChange={e => setCore('sha256', e.target.value)} />
            </div>
            <div className="flex items-center gap-3 sm:col-span-2">
              <label className="w-[72px] flex-shrink-0 text-[13px] text-ink-soft">URL</label>
              <input className="input-field flex-1 font-mono" placeholder="可选自定义下载地址（zip/tar.gz/二进制）"
                value={coreForm.url} onChange={e => setCore('url', e.target.value)} />
            </div>
          </div>

          <div className="flex flex-wrap gap-3 items-center">
            <button type="button" className="btn-primary px-5" disabled={coreBusy} onClick={fetchCores}>
              {coreBusy ? '下载中…' : '下载并缓存'}
            </button>
            <button type="button" className="btn-secondary px-4" disabled={coreBusy} onClick={loadCores}>刷新列表</button>
            <span className="text-[12px] text-ink-mut">源：XTLS/Xray-core · SagerNet/sing-box · enfein/mieru</span>
          </div>
        </div>
      </div>

      {/* 面板迁移 — separate card */}
      <div className="card mt-5" style={{ maxWidth: 980 }}>
        <div className="card-header flex items-center justify-between gap-3">
          <h3 className="text-[16px] font-bold m-0">面板迁移（换服务器）</h3>
          <button type="button" className="text-[13px] text-emerald-600 font-semibold bg-transparent border-0 cursor-pointer p-0"
            onClick={() => setMigGuideOpen(v => !v)}>
            {migGuideOpen ? '收起说明' : '展开详细说明'}
          </button>
        </div>
        <div className="px-6 py-[22px] space-y-5">
          {migGuideOpen && (
            <div className="rounded-[10px] border border-line bg-[#f7f9fc] px-4 py-3 text-[13px] text-ink-soft leading-relaxed space-y-2">
              <p className="m-0 font-semibold text-ink">用途</p>
              <p className="m-0">把整个面板（用户、规则、节点 Token、落地仓库等）迁到新机器；在线 agent 会自动改连新地址，无需一台台重装。</p>
              <p className="m-0 font-semibold text-ink pt-1">推荐步骤</p>
              <ol className="m-0 pl-5 space-y-1">
                <li>在本页点「导出数据库」，保存 <code className="font-mono text-[12px]">panel-migrate-*.db</code>。</li>
                <li>新机器执行 <code className="font-mono text-[12px]">install.sh server</code> 装好面板。</li>
                <li><code className="font-mono text-[12px]">systemctl stop nft-server</code>，用导出文件覆盖 <code className="font-mono text-[12px]">/var/lib/nft/panel.db</code>（可选同时拷贝 <code className="font-mono text-[12px]">/var/lib/nft/docs-assets/</code>）。</li>
                <li><code className="font-mono text-[12px]">systemctl start nft-server</code>，浏览器登录新面板，确认用户/节点列表与旧机一致（节点会暂时离线，正常）。</li>
                <li><strong>旧面板保持运行</strong>，在下方填写新面板地址（如 <code className="font-mono text-[12px]">https://新IP:7788</code> 或域名），点「通知 Agent 切换」。</li>
                <li>观察状态：在线节点会断开并在<strong>新面板</strong>上线；当时离线的节点连上旧面板后也会被自动补推。</li>
                <li>新面板节点都在线后，可停掉旧机，并点「清除待迁移」。</li>
              </ol>
              <p className="m-0 font-semibold text-ink pt-1">注意</p>
              <ul className="m-0 pl-5 space-y-1">
                <li>Token 随数据库迁移，<strong>不要</strong>在旧机上批量重置节点 Token。</li>
                <li>Agent 需为较新版本（支持 panel_redirect）。过旧请先在旧面板升级 agent，或对该节点手动：<code className="font-mono text-[12px]">install.sh agent --panel-url … --token …</code>。</li>
                <li>控制面建议 HTTPS/WSS。若 agent 以明文 ws 安装，需本机也允许 insecure。</li>
                <li>若面板一直用<strong>域名</strong>，也可以只改 DNS A 记录、不走本功能。</li>
                <li>组合节点请确保其入口物理机 agent 已迁过去；本机 self 节点随新面板自带。</li>
              </ul>
            </div>
          )}

          <div>
            <div className="text-[14px] font-semibold text-ink mb-1.5">1. 导出数据库</div>
            <p className="text-[12px] text-ink-mut m-0 mb-2">生成与线上一致的 SQLite 快照（含用户、规则、节点 secret 等）。请妥善保存。</p>
            <button type="button" className="btn-primary px-5" disabled={migBusy} onClick={downloadExport}>
              {migBusy ? '处理中…' : '导出数据库'}
            </button>
          </div>

          <div className="border-t border-line-soft pt-5">
            <div className="text-[14px] font-semibold text-ink mb-1.5">2. 通知 Agent 切换地址</div>
            <p className="text-[12px] text-ink-mut m-0 mb-2">
              在<strong>旧面板</strong>操作。新机需已导入上一步的库。会写入「待迁移」地址：当前在线节点立即推送，稍后连上的节点 hello 时自动补推。
            </p>
            <div className="flex flex-wrap gap-2 items-center">
              <input className="input-field font-mono flex-1 min-w-[240px] max-w-[480px]"
                placeholder="https://新面板IP:7788 或 https://panel.example.com"
                value={migURL} onChange={e => setMigURL(e.target.value)} />
              <button type="button" className="btn-primary px-5" disabled={migBusy} onClick={pushRedirect}>
                通知 Agent 切换
              </button>
              <button type="button" className="btn-secondary px-4" disabled={migBusy} onClick={loadMigStatus}>
                刷新状态
              </button>
            </div>
            {pending ? (
              <div className="mt-2 text-[12px] text-amber-700 bg-amber-50 border border-amber-200 rounded-md px-3 py-2 flex flex-wrap items-center gap-3">
                <span>待迁移地址：<span className="font-mono font-semibold">{pending}</span>（晚连上的节点仍会补推）</span>
                <button type="button" className="text-amber-800 font-semibold underline bg-transparent border-0 p-0 cursor-pointer"
                  disabled={migBusy} onClick={clearPending}>清除待迁移</button>
              </div>
            ) : (
              <p className="text-[12px] text-ink-mut mt-2 m-0">当前无待迁移地址。</p>
            )}
          </div>

          <div className="border-t border-line-soft pt-5">
            <div className="text-[14px] font-semibold text-ink mb-1.5">3. 迁移状态</div>
            {migStatus ? (
              <>
                <div className="flex flex-wrap gap-4 text-[13px] text-ink-soft mb-3">
                  <span>Hub 在线：<b className="text-ink">{migStatus.online ?? 0}</b></span>
                  <span>离线：<b className="text-ink">{migStatus.offline ?? 0}</b></span>
                  <span>最近推送成功：<b className="text-emerald-600">{migStatus.redirect_ok ?? 0}</b></span>
                  <span>失败：<b className="text-red-600">{migStatus.redirect_fail ?? 0}</b></span>
                </div>
                {Array.isArray(migStatus.nodes) && migStatus.nodes.length > 0 && (
                  <div className="overflow-x-auto rounded-[10px] border border-line">
                    <table className="tbl w-full text-[13px]">
                      <thead>
                        <tr>
                          <th className="text-left">节点</th>
                          <th className="text-left">版本</th>
                          <th className="text-left">连接</th>
                          <th className="text-left">推送结果</th>
                        </tr>
                      </thead>
                      <tbody>
                        {migStatus.nodes.map(n => (
                          <tr key={n.id}>
                            <td className="font-semibold">{n.name} <span className="text-ink-mut font-normal">#{n.id}</span></td>
                            <td className="font-mono text-xs">{n.agent_version || '—'}</td>
                            <td>{n.online ? <span className="text-emerald-600 font-semibold">在线</span> : <span className="text-ink-mut">离线</span>}</td>
                            <td className="text-xs">
                              {n.redirect_ok === true && <span className="text-emerald-600">已接受</span>}
                              {n.redirect_ok === false && <span className="text-red-600">{n.redirect_error || '失败'}</span>}
                              {n.redirect_ok == null && <span className="text-ink-mut">—</span>}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </>
            ) : (
              <p className="text-[13px] text-ink-mut m-0">加载中…</p>
            )}
          </div>
        </div>
      </div>
    </Layout>
  )
}
