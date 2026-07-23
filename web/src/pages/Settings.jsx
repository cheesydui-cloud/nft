import { useState, useEffect } from 'react'
import { api } from '../lib/api'
import { Layout, useToast } from '../components/Layout'
import { Loading } from '../components/ui'

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
  })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const toast = useToast()

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
      }))
    }).catch(e => setError(e.message)).finally(() => setLoading(false))
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
      }
      if (!form.cf_clear_token && form.cf_api_token.trim()) {
        body.cf_api_token = form.cf_api_token.trim()
      }
      await api.post('/settings', body)
      toast('设置已保存')
      // reload token status without echoing secret
      const data = await api.get('/settings')
      setForm(f => ({
        ...f,
        cf_token_configured: !!data.cf_token_configured,
        cf_token_prefix: data.cf_token_prefix || '',
        cf_api_token: '',
        cf_clear_token: false,
        cf_zone_name: data.cf_zone_name || '',
        cf_ttl: data.cf_ttl ?? 1,
      }))
    } catch (err) { setError(err.message) } finally { setSaving(false) }
  }

  if (loading) return <Layout><Loading /></Layout>

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
                用于落地仓库：目标填域名、保存时把「当前 IP」写入 CF 的 A 记录（仅 DNS / 灰云）。Token 只存服务端，接口不回显明文。
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
                <p className="text-[12px] text-ink-mut mt-1.5 m-0">落地条目未单独填 Zone 时使用。须与 Token 有权限的域名一致。</p>
              </div>
            </div>

            <div className="flex items-center gap-6 pb-[22px] border-b border-line-soft">
              <label className="w-[110px] flex-shrink-0 text-[14px] text-ink-soft">TTL</label>
              <input className="input-field w-[100px]" type="number" min="1" value={form.cf_ttl}
                onChange={e => set('cf_ttl', e.target.value)} />
              <span className="text-[13px] text-ink-mut">秒；1 = Cloudflare Auto（推荐）</span>
            </div>

            <div className="flex items-center gap-4 mt-[22px]">
              <button type="submit" disabled={saving} className="btn-primary">{saving ? '保存中…' : '保存设置'}</button>
            </div>
          </form>
        </div>
      </div>
    </Layout>
  )
}
