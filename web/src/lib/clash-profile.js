/* Build a full Clash / Mihomo profile YAML from proxy URIs for client import.
   Anti-DNS-leak defaults: fake-ip, ipv6 off, DoH nameservers, respect-rules,
   private DIRECT only (global) or CN+private DIRECT (split). */

import { uriToClashYaml } from './yaml-convert'

/**
 * @param {{ name?: string, uri: string, protocol?: string }[]} items
 * @param {{ mode?: 'global'|'split', username?: string, brand?: string }} [opt]
 * @returns {{ yaml: string, count: number, skipped: number }}
 */
export function buildClashProfile(items, opt = {}) {
  const mode = opt.mode === 'split' ? 'split' : 'global'
  const user = (opt.username || 'user').trim() || 'user'
  const brand = (opt.brand || 'nft-panel').trim() || 'nft-panel'

  const proxies = []
  const names = []
  const seen = Object.create(null)
  let skipped = 0

  for (const it of items || []) {
    const uri = (it?.uri || '').trim()
    if (!uri) continue
    const proto = String(it.protocol || '').toLowerCase()
    const lower = uri.toLowerCase()
    if (proto === 'mieru' || lower.startsWith('mierus://') || lower.startsWith('mieru://')) {
      skipped++
      continue
    }
    let name = (it.name || '').trim() || nameFromURI(uri) || 'proxy'
    const base = name
    if (seen[base]) {
      seen[base]++
      name = `${base}-${seen[base]}`
    } else {
      seen[base] = 1
    }
    const yml = uriToClashYaml(uri)
    if (!yml) {
      skipped++
      continue
    }
    // Force display name in first line when converter used fragment name.
    const lines = yml.split('\n')
    if (lines[0] && lines[0].startsWith('- name:')) {
      lines[0] = `- name: "${esc(name)}"`
    }
    proxies.push(lines.join('\n'))
    names.push(name)
  }

  const L = []
  if (mode === 'global') {
    L.push(`# Mihomo / Clash Meta – 全局防泄漏 (${brand})`)
    L.push(`# user: ${user}`)
    L.push('# purpose: full tunnel · no China DIRECT · DNS via proxy (respect-rules)')
    L.push('# 导入: Clash Verge / Mihomo → 配置 → 导入文件或粘贴')
    L.push('# 客户端模式建议 Rule；系统代理/TUN 任选其一')
  } else {
    L.push(`# Mihomo / Clash Meta – 分流 (${brand})`)
    L.push(`# user: ${user}`)
    L.push('# routing: CN + private = DIRECT；其余 = PROXY')
    L.push('# 客户端模式必须为 Rule（不要用 Global）')
  }
  if (skipped > 0) {
    L.push(`# note: ${skipped} 条未写入 Clash（mieru 或不支持协议，请用 URI/普通订阅）`)
  }
  if (names.length === 0) {
    L.push('# warning: 当前列表没有 Clash 可识别节点（ss/vless/vmess/trojan/hy2）')
  }
  L.push('#')
  L.push('mixed-port: 7890')
  L.push('allow-lan: false')
  L.push('mode: rule')
  L.push('log-level: info')
  L.push('ipv6: false')
  L.push('unified-delay: true')
  L.push('tcp-concurrent: true')
  L.push('find-process-mode: off')
  L.push('')
  // DNS anti-leak block
  L.push('dns:')
  L.push('  enable: true')
  L.push('  ipv6: false')
  L.push('  enhanced-mode: fake-ip')
  L.push('  fake-ip-range: 198.18.0.1/16')
  L.push('  fake-ip-filter:')
  L.push('    - "*.lan"')
  L.push('    - "*.local"')
  L.push('    - "localhost.ptlogin2.qq.com"')
  L.push('    - "+.srv.nintendo.net"')
  L.push('    - "+.stun.playstation.net"')
  L.push('    - "xbox.*.microsoft.com"')
  L.push('    - "+.xboxlive.com"')
  L.push('  use-hosts: true')
  L.push('  default-nameserver:')
  L.push('    - 223.5.5.5')
  L.push('    - 8.8.8.8')
  // Resolve proxy server domains without going through the tunnel (bootstrap).
  L.push('  proxy-server-nameserver:')
  L.push('    - https://dns.alidns.com/dns-query')
  L.push('    - https://doh.pub/dns-query')
  if (mode === 'global') {
    // All application DNS via DoH; respect-rules forces DNS queries through PROXY rules → anti-leak.
    L.push('  nameserver:')
    L.push('    - https://1.1.1.1/dns-query')
    L.push('    - https://8.8.8.8/dns-query')
    L.push('  respect-rules: true')
  } else {
    L.push('  nameserver:')
    L.push('    - https://dns.alidns.com/dns-query')
    L.push('    - https://doh.pub/dns-query')
    L.push('  fallback:')
    L.push('    - https://1.1.1.1/dns-query')
    L.push('    - https://8.8.8.8/dns-query')
    L.push('  fallback-filter:')
    L.push('    geoip: true')
    L.push('    geoip-code: CN')
    L.push('  respect-rules: true')
  }
  L.push('')
  L.push('proxies:')
  if (proxies.length === 0) {
    L.push('  []')
  } else {
    for (const p of proxies) {
      for (const line of p.split('\n')) {
        if (line === '') continue
        L.push(`  ${line}`)
      }
    }
  }
  L.push('')
  L.push('proxy-groups:')
  L.push('  - name: PROXY')
  L.push('    type: select')
  L.push('    proxies:')
  if (names.length === 0) {
    L.push('      - DIRECT')
  } else {
    for (const n of names) L.push(`      - ${yamlName(n)}`)
    L.push('      - AUTO')
    L.push('      - DIRECT')
    L.push('  - name: AUTO')
    L.push('    type: url-test')
    L.push('    url: http://www.gstatic.com/generate_204')
    L.push('    interval: 300')
    L.push('    tolerance: 50')
    L.push('    proxies:')
    for (const n of names) L.push(`      - ${yamlName(n)}`)
  }
  L.push('')
  L.push('rules:')
  // Always keep LAN/private off the tunnel.
  L.push('  - DOMAIN-SUFFIX,local,DIRECT')
  L.push('  - DOMAIN-SUFFIX,localhost,DIRECT')
  L.push('  - IP-CIDR,127.0.0.0/8,DIRECT,no-resolve')
  L.push('  - IP-CIDR,10.0.0.0/8,DIRECT,no-resolve')
  L.push('  - IP-CIDR,172.16.0.0/12,DIRECT,no-resolve')
  L.push('  - IP-CIDR,192.168.0.0/16,DIRECT,no-resolve')
  L.push('  - IP-CIDR,169.254.0.0/16,DIRECT,no-resolve')
  L.push('  - GEOIP,PRIVATE,DIRECT,no-resolve')
  if (mode === 'split') {
    L.push('  - GEOIP,CN,DIRECT')
  }
  L.push('  - MATCH,PROXY')
  L.push('')

  return { yaml: L.join('\n'), count: names.length, skipped }
}

function nameFromURI(uri) {
  const h = uri.indexOf('#')
  if (h < 0) return ''
  try {
    return decodeURIComponent(uri.slice(h + 1))
  } catch {
    return uri.slice(h + 1)
  }
}

function esc(s) {
  return String(s || '').replace(/\\/g, '\\\\').replace(/"/g, '\\"')
}

function yamlName(n) {
  // Quote when needed (spaces, special chars).
  if (/^[A-Za-z0-9_.\-\u4e00-\u9fff]+$/.test(n)) return n
  return `"${esc(n)}"`
}

export function downloadTextFile(filename, text, mime = 'text/yaml;charset=utf-8') {
  const blob = new Blob([text], { type: mime })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(a.href), 2000)
}
