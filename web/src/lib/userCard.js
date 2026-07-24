/* Admin-facing user delivery card (微信/飞书可直接粘贴).
   Password is always the panel's published initial password — not a live hash. */

import { fmtDate, fmtTrafficGB, nullInt } from './fmt'
import { formatRuleCopyText } from './relayCopy'

/** Published initial password shown on every user card. */
export const CARD_INITIAL_PASSWORD = '123456'

/**
 * Build plain-text user card for clipboard delivery.
 * @param {object} opts
 * @param {string} [opts.panelName]
 * @param {string} opts.panelURL
 * @param {object} opts.user - list or detail user object
 * @param {object[]} [opts.rules] - rules with relay_uri when available
 * @param {Map|object} [opts.expiryMap] - host:port → expires_at fallback
 * @param {boolean} [opts.asYaml]
 * @param {string} [opts.password] - defaults to CARD_INITIAL_PASSWORD
 */
export function buildUserCardText({
  panelName = '',
  panelURL = '',
  user,
  rules = [],
  expiryMap,
  asYaml = false,
  password = CARD_INITIAL_PASSWORD,
} = {}) {
  if (!user) return ''
  const brand = (panelName || '').trim() || 'nft'
  const url = (panelURL || '').trim() || (typeof window !== 'undefined' ? window.location.origin : '')
  const username = user.username || ''
  const exp = nullInt(user.expires_at) || (user.expires_at > 0 ? user.expires_at : 0)
  const expiresLine = exp > 0 ? fmtDate(exp) : '永不过期'
  const used = user.traffic_used_bytes || 0
  const quota = user.traffic_quota_bytes || 0
  const rate = user.billing_rate > 0 ? user.billing_rate : 1
  const trafficLine = fmtTrafficGB(Math.round(used * rate), quota)
  const maxFwd = user.max_forwards > 0 ? String(user.max_forwards) : '不限'
  const ruleCount = Array.isArray(rules) ? rules.length : (user.rule_count || 0)
  const speed = user.speed_limit_mbytes > 0 ? `${user.speed_limit_mbytes} Mbps` : '不限'

  const lines = [
    `【${brand}】账号信息`,
    '',
    `面板地址：${url}`,
    `用户名：${username}`,
    `初始密码：${password}`,
    '',
    `到期时间：${expiresLine}`,
    `流量配额：${trafficLine}`,
    `规则：${ruleCount} / ${maxFwd}`,
    `限速：${speed}`,
  ]

  if (user.disabled) {
    lines.push('状态：已禁用（请联系管理员）')
  }

  const proxyLines = []
  for (const r of rules || []) {
    const text = formatRuleCopyText(r, {
      username,
      expiryMap,
      asYaml,
    })
    if (text) proxyLines.push(text)
  }
  if (proxyLines.length) {
    lines.push('', '—— 代理链接（可直接导入）——', ...proxyLines)
  }

  lines.push(
    '',
    '使用说明：登录面板后可在「我的代理」复制或扫码导入；初始密码登录后请自行修改。',
  )
  return lines.join('\n')
}

/** Resolve panel URL/name for cards (settings first, then branding/origin). */
export async function loadPanelBranding(api) {
  let panelURL = ''
  let panelName = ''
  try {
    const s = await api.get('/settings')
    panelURL = (s?.panel_url || '').trim()
    panelName = (s?.panel_name || '').trim()
  } catch { /* ignore */ }
  if (!panelURL && typeof window !== 'undefined') panelURL = window.location.origin
  if (!panelName) {
    try {
      const b = await api.get('/branding')
      panelName = (b?.panel_name || '').trim()
    } catch { /* ignore */ }
  }
  return { panelURL, panelName }
}
