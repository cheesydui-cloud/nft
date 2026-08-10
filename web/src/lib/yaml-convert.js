/* Convert a proxy URI to Clash YAML proxy config. Returns null for
   unsupported/non-URI formats (snell config lines, etc.) so callers
   can fall back to copying the raw URI. */

export function uriToClashYaml(uri) {
  if (!uri || typeof uri !== 'string') return null
  const raw = uri.trim()
  if (!raw) return null
  const i = raw.indexOf('://')
  if (i <= 0) return null
  const scheme = raw.slice(0, i).toLowerCase()
  switch (scheme) {
    case 'ss':
    case 'shadowsocks':
      return ssToYaml(raw)
    case 'vmess': return vmessToYaml(raw)
    case 'vless': return vlessToYaml(raw)
    case 'trojan': return trojanToYaml(raw)
    case 'hy2':
    case 'hysteria2': return hy2ToYaml(raw)
    case 'mieru':
    case 'mierus':
      return mieruToYaml(raw)
    default: return null
  }
}

/** Mihomo native mieru (Meta). Official share: mierus://user:pass@host?port=P&protocol=TCP */
function mieruToYaml(uri) {
  const i = uri.indexOf('://')
  let rest = uri.slice(i + 3)
  let name = ''
  const h = rest.indexOf('#')
  if (h >= 0) { name = dec(rest.slice(h + 1)); rest = rest.slice(0, h) }
  let params = {}
  const q = rest.indexOf('?')
  if (q >= 0) {
    params = parseQSMulti(rest.slice(q + 1))
    rest = rest.slice(0, q)
  }
  let username = '', password = ''
  const at = rest.lastIndexOf('@')
  if (at >= 0) {
    const userinfo = rest.slice(0, at)
    const colon = userinfo.indexOf(':')
    if (colon >= 0) {
      username = dec(userinfo.slice(0, colon))
      password = dec(userinfo.slice(colon + 1))
    } else {
      username = dec(userinfo)
    }
    rest = rest.slice(at + 1)
  }
  // Authority is usually host only; port lives in query (may appear multiple times).
  let host = rest
  let port = 0
  if (host.startsWith('[')) {
    const close = host.indexOf(']')
    if (close > 0) host = host.slice(1, close)
  } else {
    const c = host.lastIndexOf(':')
    if (c > 0 && /^\d+$/.test(host.slice(c + 1))) {
      port = Number(host.slice(c + 1))
      host = host.slice(0, c)
    }
  }
  const ports = [].concat(params.port || []).map(Number).filter(n => n > 0 && n <= 65535)
  if (!port && ports.length) port = ports[0]
  if (!host || !port) return null
  if (!username || !password) return null

  // protocol may be multi: TCP,UDP — Clash field is singular; prefer TCP then UDP.
  const protos = [].concat(params.protocol || []).map(p => String(p).toUpperCase())
  let transport = 'TCP'
  if (protos.includes('TCP')) transport = 'TCP'
  else if (protos.includes('UDP')) transport = 'UDP'
  else if (protos[0]) transport = protos[0]

  if (!name) name = params.profile || host

  const L = []
  L.push(`- name: "${esc(name)}"`)
  L.push(`  type: mieru`)
  L.push(`  server: ${host}`)
  L.push(`  port: ${port}`)
  L.push(`  transport: ${transport}`)
  L.push(`  username: "${esc(username)}"`)
  L.push(`  password: "${esc(password)}"`)
  L.push(`  multiplexing: MULTIPLEXING_LOW`)
  if (params['traffic-pattern'] || params.trafficPattern) {
    const tp = params['traffic-pattern'] || params.trafficPattern
    const v = Array.isArray(tp) ? tp[0] : tp
    if (v) L.push(`  traffic-pattern: "${esc(v)}"`)
  }
  return L.join('\n')
}

/** Like parseQS but keeps multi-value keys as arrays (mieru port/protocol). */
function parseQSMulti(qs) {
  const m = {}
  for (const p of String(qs || '').split('&')) {
    if (!p) continue
    const eq = p.indexOf('=')
    const k = eq >= 0 ? dec(p.slice(0, eq)) : dec(p)
    const v = eq >= 0 ? dec(p.slice(eq + 1)) : ''
    if (!k) continue
    if (m[k] === undefined) m[k] = v
    else if (Array.isArray(m[k])) m[k].push(v)
    else m[k] = [m[k], v]
  }
  return m
}

function ssToYaml(uri) {
  // Accept ss:// and shadowsocks://
  const schemeEnd = uri.indexOf('://')
  let rest = uri.slice(schemeEnd + 3)
  let name = ''
  const h = rest.indexOf('#')
  if (h >= 0) { name = dec(rest.slice(h + 1)); rest = rest.slice(0, h) }

  // SIP002: ss://userinfo@host:port/?plugin=…  or  …/path?plugin=…
  // Strip query first, then any path after host:port so hostport works.
  let plugin = '', pluginOpts = ''
  const q = rest.indexOf('?')
  if (q >= 0) {
    const params = parseQS(rest.slice(q + 1))
    plugin = params.plugin || ''
    pluginOpts = params['plugin-opts'] || ''
    rest = rest.slice(0, q)
  }
  // Drop path segment (e.g. trailing "/" after port)
  const pathSlash = rest.indexOf('/')
  if (pathSlash >= 0) {
    // Only strip path when it is after authority (has @ or looks like host:port/)
    const atProbe = rest.lastIndexOf('@')
    if (atProbe >= 0 || /^\[[^\]]+\]:\d+\//.test(rest) || /^[^@/]+:\d+\//.test(rest)) {
      rest = rest.slice(0, pathSlash)
    }
  }

  let method, password, host, port
  const at = rest.lastIndexOf('@')
  if (at >= 0) {
    let userinfo = rest.slice(0, at)
    // Prefer base64(method:password); else plain / percent-decoded method:password
    const decoded = b64(userinfo)
    if (decoded && decoded.includes(':')) {
      userinfo = decoded
    } else {
      const plain = dec(userinfo)
      if (plain.includes(':')) userinfo = plain
    }
    const colon = userinfo.indexOf(':')
    if (colon < 0) return null
    method = userinfo.slice(0, colon).trim()
    password = userinfo.slice(colon + 1)
    const hp = hostport(rest.slice(at + 1))
    if (!hp) return null
    host = hp[0]; port = hp[1]
  } else {
    // Legacy: entire body base64(method:password@host:port)
    const decoded = b64(rest)
    if (!decoded) return null
    const at2 = decoded.lastIndexOf('@')
    if (at2 < 0) return null
    const colon = decoded.indexOf(':')
    if (colon < 0 || colon > at2) return null
    method = decoded.slice(0, colon).trim()
    password = decoded.slice(colon + 1, at2)
    const hp = hostport(decoded.slice(at2 + 1))
    if (!hp) return null
    host = hp[0]; port = hp[1]
  }
  if (!method || !host || !port) return null

  const L = []
  L.push(`- name: "${esc(name)}"`)
  L.push(`  type: ss`)
  L.push(`  server: ${host}`)
  L.push(`  port: ${port}`)
  L.push(`  cipher: ${method}`)
  L.push(`  password: "${esc(password)}"`)
  L.push(`  udp: true`)
  // plugin=obfs-local;obfs=http;obfs-host=…  → Clash simple plugin fields when possible
  if (plugin) {
    const mapped = mapSSPlugin(plugin)
    if (mapped) {
      L.push(`  plugin: ${mapped.name}`)
      if (mapped.opts && Object.keys(mapped.opts).length) {
        L.push(`  plugin-opts:`)
        for (const [k, v] of Object.entries(mapped.opts)) {
          L.push(`    ${k}: ${yamlScalar(v)}`)
        }
      }
    } else {
      L.push(`  plugin: ${plugin}`)
      if (pluginOpts) L.push(`  plugin-opts: ${pluginOpts}`)
    }
  }
  return L.join('\n')
}

/** Map SIP002 plugin string to Clash Meta plugin name + opts. */
function mapSSPlugin(raw) {
  const s = String(raw || '').trim()
  if (!s) return null
  // "obfs-local;obfs=http;obfs-host=www.example.com"
  const parts = s.split(';').map(x => x.trim()).filter(Boolean)
  if (!parts.length) return null
  const head = parts[0].toLowerCase()
  const opts = {}
  for (let i = 1; i < parts.length; i++) {
    const eq = parts[i].indexOf('=')
    if (eq > 0) opts[parts[i].slice(0, eq)] = parts[i].slice(eq + 1)
  }
  if (head === 'obfs-local' || head === 'simple-obfs' || head === 'obfs') {
    const mode = opts.obfs || opts.mode || 'http'
    const out = { name: 'obfs', opts: { mode } }
    if (opts['obfs-host'] || opts.host) out.opts.host = opts['obfs-host'] || opts.host
    return out
  }
  if (head === 'v2ray-plugin') {
    const out = { name: 'v2ray-plugin', opts: {} }
    if (opts.mode) out.opts.mode = opts.mode
    if (opts.tls === 'true' || opts.tls === '1') out.opts.tls = true
    if (opts.host) out.opts.host = opts.host
    if (opts.path) out.opts.path = opts.path
    return out
  }
  return null
}

function yamlScalar(v) {
  if (typeof v === 'boolean') return v ? 'true' : 'false'
  const s = String(v)
  if (/^[A-Za-z0-9_./-]+$/.test(s)) return s
  return `"${esc(s)}"`
}

function vmessToYaml(uri) {
  const decoded = b64(uri.slice('vmess://'.length))
  if (!decoded) return null
  let m
  try { m = JSON.parse(decoded) } catch { return null }
  const L = []
  L.push(`- name: "${esc(m.ps || '')}"`)
  L.push(`  type: vmess`)
  L.push(`  server: ${m.add}`)
  L.push(`  port: ${m.port}`)
  L.push(`  uuid: ${m.id}`)
  L.push(`  alterId: ${m.aid || 0}`)
  L.push(`  cipher: ${m.scy || 'auto'}`)
  const net = m.net || 'tcp'
  if (net !== 'tcp') L.push(`  network: ${net}`)
  if (m.tls === 'tls') L.push(`  tls: true`)
  if (m.sni) L.push(`  servername: ${m.sni}`)
  if (net === 'ws') {
    L.push(`  ws-opts:`)
    if (m.path) L.push(`    path: "${esc(m.path)}"`)
    if (m.host) { L.push(`    headers:`); L.push(`      Host: ${m.host}`) }
  } else if (net === 'grpc' && m.path) {
    L.push(`  grpc-opts:`)
    L.push(`    grpc-service-name: "${esc(m.path)}"`)
  }
  L.push(`  udp: true`)
  return L.join('\n')
}

function vlessToYaml(uri) {
  const i = uri.indexOf('://')
  let rest = uri.slice(i + 3)
  let name = ''
  const h = rest.indexOf('#')
  if (h >= 0) { name = dec(rest.slice(h + 1)); rest = rest.slice(0, h) }
  let params = {}
  const q = rest.indexOf('?')
  if (q >= 0) { params = parseQS(rest.slice(q + 1)); rest = rest.slice(0, q) }
  const at = rest.indexOf('@')
  if (at < 0) return null
  const uuid = rest.slice(0, at)
  const hp = hostport(rest.slice(at + 1))
  if (!hp) return null
  const L = []
  L.push(`- name: "${esc(name)}"`)
  L.push(`  type: vless`)
  L.push(`  server: ${hp[0]}`)
  L.push(`  port: ${hp[1]}`)
  L.push(`  uuid: ${uuid}`)
  if (params.flow) L.push(`  flow: ${params.flow}`)
  if (params.security === 'tls' || params.security === 'reality') L.push(`  tls: true`)
  if (params.sni) L.push(`  servername: ${params.sni}`)
  if (params.fp) L.push(`  client-fingerprint: ${params.fp}`)
  if (params.security === 'reality') {
    L.push(`  reality-opts:`)
    if (params.pbk) L.push(`    public-key: ${params.pbk}`)
    if (params.sid) L.push(`    short-id: ${params.sid}`)
  }
  const net = params.type || 'tcp'
  if (net !== 'tcp') L.push(`  network: ${net}`)
  appendTransport(L, net, params)
  L.push(`  udp: true`)
  return L.join('\n')
}

function trojanToYaml(uri) {
  const i = uri.indexOf('://')
  let rest = uri.slice(i + 3)
  let name = ''
  const h = rest.indexOf('#')
  if (h >= 0) { name = dec(rest.slice(h + 1)); rest = rest.slice(0, h) }
  let params = {}
  const q = rest.indexOf('?')
  if (q >= 0) { params = parseQS(rest.slice(q + 1)); rest = rest.slice(0, q) }
  const at = rest.indexOf('@')
  if (at < 0) return null
  const password = rest.slice(0, at)
  const hp = hostport(rest.slice(at + 1))
  if (!hp) return null
  const L = []
  L.push(`- name: "${esc(name)}"`)
  L.push(`  type: trojan`)
  L.push(`  server: ${hp[0]}`)
  L.push(`  port: ${hp[1]}`)
  L.push(`  password: "${esc(password)}"`)
  if (params.sni) L.push(`  sni: ${params.sni}`)
  if (params.fp) L.push(`  client-fingerprint: ${params.fp}`)
  const net = params.type || 'tcp'
  if (net !== 'tcp') L.push(`  network: ${net}`)
  appendTransport(L, net, params)
  L.push(`  udp: true`)
  return L.join('\n')
}

function hy2ToYaml(uri) {
  const i = uri.indexOf('://')
  let rest = uri.slice(i + 3)
  let name = ''
  const h = rest.indexOf('#')
  if (h >= 0) { name = dec(rest.slice(h + 1)); rest = rest.slice(0, h) }
  let params = {}
  const q = rest.indexOf('?')
  if (q >= 0) { params = parseQS(rest.slice(q + 1)); rest = rest.slice(0, q) }
  let auth = ''
  const at = rest.indexOf('@')
  if (at >= 0) { auth = rest.slice(0, at); rest = rest.slice(at + 1) }
  const hp = hostport(rest)
  if (!hp) return null
  const L = []
  L.push(`- name: "${esc(name)}"`)
  L.push(`  type: hysteria2`)
  L.push(`  server: ${hp[0]}`)
  L.push(`  port: ${hp[1]}`)
  if (auth) L.push(`  password: "${esc(auth)}"`)
  if (params.sni) L.push(`  sni: ${params.sni}`)
  if (params.insecure === '1') L.push(`  skip-cert-verify: true`)
  if (params.obfs === 'salamander' && params['obfs-password']) {
    L.push(`  obfs: salamander`)
    L.push(`  obfs-password: "${esc(params['obfs-password'])}"`)
  }
  return L.join('\n')
}

function appendTransport(L, net, params) {
  if (net === 'ws') {
    L.push(`  ws-opts:`)
    if (params.path) L.push(`    path: "${esc(params.path)}"`)
    if (params.host) { L.push(`    headers:`); L.push(`      Host: ${params.host}`) }
  } else if (net === 'grpc' && params.serviceName) {
    L.push(`  grpc-opts:`)
    L.push(`    grpc-service-name: "${esc(params.serviceName)}"`)
  }
}

function parseQS(qs) {
  const m = {}
  for (const p of qs.split('&')) {
    const eq = p.indexOf('=')
    if (eq > 0) m[p.slice(0, eq)] = dec(p.slice(eq + 1))
  }
  return m
}

function hostport(s) {
  if (!s) return null
  // Strip path/query leftovers: host:port/ or host:port/path
  let raw = String(s).trim()
  const slash = raw.indexOf('/')
  if (slash >= 0) raw = raw.slice(0, slash)
  const q = raw.indexOf('?')
  if (q >= 0) raw = raw.slice(0, q)
  let host, portStr
  if (raw.startsWith('[')) {
    const close = raw.indexOf(']')
    if (close < 0) return null
    host = raw.slice(1, close)
    const rem = raw.slice(close + 1)
    if (!rem.startsWith(':')) return null
    portStr = rem.slice(1)
  } else {
    const c = raw.lastIndexOf(':')
    if (c < 0) return null
    host = raw.slice(0, c)
    portStr = raw.slice(c + 1)
  }
  const port = Number(portStr)
  if (!host || !Number.isInteger(port) || port < 1 || port > 65535) return null
  return [host, port]
}

function esc(s) { return (s || '').replace(/\\/g, '\\\\').replace(/"/g, '\\"') }
function dec(s) { try { return decodeURIComponent(s) } catch { return s } }
function b64(s) {
  const candidates = [s, s.replace(/-/g, '+').replace(/_/g, '/')]
  for (const v of candidates) {
    const pad = v.length % 4 ? '='.repeat(4 - (v.length % 4)) : ''
    try {
      const bin = atob(v + pad)
      try { return decodeURIComponent(escape(bin)) } catch { return bin }
    } catch { /* next */ }
  }
  return null
}
