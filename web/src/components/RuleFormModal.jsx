import { useState, useEffect, useMemo, Fragment } from 'react'
import { Modal, Select, ProbeButton, nodeStack, NodeTypeIcon } from './ui'
import { useToast } from './Layout'
import { tryParseURI } from '../lib/landing'
import { fmtDate } from '../lib/fmt'
import { api } from '../lib/api'

const EMPTY = {
  node_id: '', owner_id: 0, name: '', proto: 'tcp', exit: '', exit_kind: 'custom',
  exit_type: 'direct', exit_uri: '',
  entry_port: '', comment: '', mode: 'kernel', via_node_ids: [],
  proxy_service_id: 0,
}

const PROTO_LABEL = {
  vless: 'VLESS',
  shadowsocks: 'Shadowsocks',
  mieru: 'mieru',
  socks5: 'SOCKS5',
  socks: 'SOCKS5',
  anytls: 'AnyTLS',
  naive: 'Naive',
  naiveproxy: 'Naive',
}

/* Shared create/edit form for forwarding rules, used by both the admin
   (`/rules`) and user (`/my/rules`) pages so create, edit and copy share one
   layout. The parent owns the API call via onSubmit(form) -> Promise (it knows
   create vs edit and admin vs user endpoint); this component only manages form
   state, the single/composite node grouping and validation. `initial` seeds the
   fields for edit/copy prefills and is re-applied every time the modal opens.

   variant: 'port' | 'chain' | undefined
     - port: single-hop only (no composite entry, no via cascade, no SK5)
     - chain: via/composite allowed; exit may be direct host:port or SOCKS5
     - undefined: legacy full form (both)

   When `landingNodes` is provided (the user side passes the merged landing-node
   list — admin-assigned plus the user's own browser-local URIs, even when
   empty), the exit gains a custom/landing toggle: a landing exit picks a node's
   host:port, a custom exit takes a host:port. The user's proxy URIs never leave
   the browser, so the modal only deals in host:port here; the rules page
   resolves the relay URI client-side. Admin callers omit the prop and keep the
   plain host:port box. */
export function RuleFormModal({ open, onClose, title, submitLabel = '保存', nodes = [], landingNodes, bindings = [], initial, onSubmit, onAddProxyURI, showRate, showStack = true, users, variant, proxyNodeIds, proxyServiceIds }) {
  const [form, setForm] = useState(EMPTY)
  const [loading, setLoading] = useState(false)
  // When parent does not fix variant, allow in-modal 端口/链式 switch (admin user-create).
  const [localVariant, setLocalVariant] = useState(variant === 'chain' ? 'chain' : 'port')
  // Select value for 选择线路: plain node_id or proxy:<serviceId>:<nodeId>.
  // Must match option values so 代理 tab labels stick (form only stores node_id).
  const [entrySelectValue, setEntrySelectValue] = useState('')
  const [proxyServices, setProxyServices] = useState([])
  const [repoSocksNodes, setRepoSocksNodes] = useState([]) // 落地仓库 socks5 节点（admin）
  const toast = useToast()

  const landingEnabled = Array.isArray(landingNodes)
  const effectiveVariant = variant === 'port' || variant === 'chain' ? variant : localVariant
  const allowTypeSwitch = variant !== 'port' && variant !== 'chain'
  const proxyIds = useMemo(() => (
    proxyNodeIds instanceof Set ? proxyNodeIds : new Set((proxyNodeIds || []).map(Number))
  ), [proxyNodeIds])
  // When parent supplies proxyServiceIds (user-scoped), only those services appear
  // on the 代理 tab. Admin lists omit the prop → all services stay visible.
  const grantedSvcIds = useMemo(() => {
    if (proxyServiceIds == null) return null // unrestricted (admin)
    return new Set((proxyServiceIds instanceof Set ? [...proxyServiceIds] : (proxyServiceIds || [])).map(Number).filter(id => id > 0))
  }, [proxyServiceIds])

  // Admin surfaces can load 代理服务 for service-name labels on the 代理 tab.
  useEffect(() => {
    if (!open) return
    let cancelled = false
    api.get('/proxy-services')
      .then(d => { if (!cancelled) setProxyServices(d?.services || []) })
      .catch(() => { if (!cancelled) setProxyServices([]) })
    return () => { cancelled = true }
  }, [open])

  // 链式 SK5 候选：管理员从落地仓库；用户端从已分配落地（landingNodes）筛 SOCKS5。
  useEffect(() => {
    if (!open) return
    const isSocks = (n) => {
      const p = String(n.protocol || '').toLowerCase().replace(/[^a-z0-9]/g, '')
      const uri = String(n.uri || '').trim().toLowerCase()
      if (p === 'socks5' || p === 'socks' || p === 'sk5' || p === 'sock') return true
      if (uri.startsWith('socks5://') || uri.startsWith('socks://')) return true
      const blob = `${n.name || ''} ${n.remark || ''}`.toLowerCase()
      if ((blob.includes('socks') || blob.includes('sk5')) && n.host && n.port) return true
      return false
    }
    // 用户端（showStack=false）：只用管理员分配的落地，禁止手填、不读仓库。
    if (showStack === false) {
      const fromLanding = (landingNodes || []).filter(isSocks).map((n, i) => ({
        ...n,
        id: n.id ?? `land-${n.host}-${n.port}-${i}`,
      }))
      setRepoSocksNodes(fromLanding)
      return
    }
    let cancelled = false
    api.get('/node-repo')
      .then(d => {
        if (cancelled) return
        setRepoSocksNodes((d?.nodes || []).filter(isSocks))
      })
      .catch(() => { if (!cancelled) setRepoSocksNodes([]) })
    return () => { cancelled = true }
  }, [open, showStack, landingNodes])

  useEffect(() => {
    if (!open) return
    const seed = { ...EMPTY, ...(initial || {}) }
    // A landing exit whose node no longer resolves stays in landing mode
    // so the picker shows empty and the user can re-select.
    if (seed.exit_kind === 'landing' && landingEnabled &&
        !landingNodes.some(n => `${n.host}:${n.port}` === seed.exit)) {
      // keep as landing — the picker will show empty
    }
    // When landing is enabled, force default to landing (no custom option)
    if (landingEnabled && seed.exit_kind === 'custom') {
      seed.exit_kind = 'landing'
    }
    setForm(seed)
    // Prefer proxy:<svc>:<node> so edit form shows the same 代理 label as create.
    const sid = Number(seed.proxy_service_id) || 0
    const nid = seed.node_id != null && seed.node_id !== '' ? String(seed.node_id) : ''
    setEntrySelectValue(sid > 0 && nid ? `proxy:${sid}:${nid}` : nid)
    if (allowTypeSwitch) {
      // Prefill chain when editing a chain-like rule; default create to port.
      const looksChain = (seed.via_node_ids || []).length > 0
        || nodes.find(n => String(n.id) === String(seed.node_id))?.node_type === 'composite'
        || seed.exit_type === 'socks5'
      setLocalVariant(looksChain ? 'chain' : 'port')
    }
  }, [open])

  const set = (k, v) => setForm(f => ({ ...f, [k]: v }))

  const switchLocalVariant = (next) => {
    if (next === localVariant) return
    setLocalVariant(next)
    setForm(f => ({
      ...f,
      // 端口转发也允许组合入口；仅清空 via 级联与 SK5 出口字段。
      via_node_ids: next === 'port' ? [] : f.via_node_ids,
      exit_type: next === 'port' ? 'direct' : f.exit_type,
      exit_uri: next === 'port' ? '' : f.exit_uri,
    }))
  }

  // Switching the entry invalidates any chosen middle-layer chain — the
  // binding graph downstream of the old entry has nothing to do with the
  // new one. This lives in the picker's own onChange rather than an effect
  // keyed on form.node_id: the seed effect above also assigns node_id (from
  // `initial`, together with its own via_node_ids) every time the modal
  // opens, and an effect can't tell that assignment apart from a real user
  // switch — it would wipe the edit prefill's chain right after seeding it.
  // Select fires onChange even when the clicked option is already selected,
  // and it emits string values while a seeded node_id may be a number — the
  // String() comparison keeps a same-entry reselect from clearing the chain.
  const pickEntry = (v) => {
    // 代理 tab 选项 value 为 proxy:<serviceId>:<nodeId>；表单存 node_id + proxy_service_id。
    // entrySelectValue 保留完整 option value，选什么就显示什么，不回落到单点标签。
    let nodeId = v
    let svcId = 0
    const s = String(v || '')
    if (s.startsWith('proxy:')) {
      const parts = s.split(':')
      svcId = Number(parts[1]) || 0
      nodeId = parts[2] || parts[parts.length - 1]
    }
    setEntrySelectValue(s)
    setForm(f => {
      const sameNode = String(nodeId) === String(f.node_id)
      const sameSvc = Number(svcId) === Number(f.proxy_service_id || 0)
      if (sameNode && sameSvc) return f
      return {
        ...f,
        node_id: nodeId,
        proxy_service_id: svcId,
        // 换节点才清 via；同节点换代理服务保留中间层
        via_node_ids: sameNode ? f.via_node_ids : [],
      }
    })
  }

  const handleExitBlur = () => {
    if (!landingEnabled || form.exit_kind !== 'custom') return
    const val = form.exit.trim()
    if (!val.includes('://')) return
    const node = tryParseURI(val)
    if (!node) return
    const hp = node.host.includes(':') ? `[${node.host}]:${node.port}` : `${node.host}:${node.port}`
    if (onAddProxyURI) onAddProxyURI(val)
    setForm(f => {
      const next = { ...f, exit_kind: 'landing', exit: hp }
      if (!f.comment.trim() && node.name) next.comment = node.name
      return next
    })
    toast(`已识别 ${node.protocol} 代理并保存`)
  }

  const isPort = effectiveVariant === 'port'
  const isChain = effectiveVariant === 'chain'
  const isSocks = isChain && form.exit_type === 'socks5'

  const submit = async (e) => {
    e.preventDefault()
    if (!form.node_id) { toast('请选择节点', 'error'); return }
    if (isPort) {
      // 端口转发也允许选组合入口（走组合内置 hop，不展示 via 级联）。
      if ((form.via_node_ids || []).length > 0) {
        toast('端口转发不支持额外线路层；请使用「链式转发」', 'error')
        return
      }
    }
    if (isSocks) {
      if (!form.exit_uri?.trim()) {
        toast(showStack === false
          ? '请从列表选择 SOCKS5 落地节点'
          : '请填写 SOCKS5 代理 URI', 'error')
        return
      }
      // 代理入口 + SK5：协议面为开放代理，CONNECT 可空（用 SK5 host:port 占位入库）。
      // 纯 L4+ExitProxy 路径仍要求显式 CONNECT 业务目标。
      // 用户端（showStack=false）不展示 CONNECT 手填，允许空并用 SK5 host:port 占位。
      if (!form.exit?.trim() && !(Number(form.proxy_service_id) > 0) && showStack !== false) {
        toast('请填写 CONNECT 目标 host:port', 'error')
        return
      }
      if (form.proto !== 'tcp') { toast('SOCKS5 出口仅支持 TCP', 'error'); return }
    }
    if (tailNoDirect) { toast(`节点 ${tailNode.name} 禁止直接转发，必须在其后选择线路层`, 'error'); return }
    if (landingEnabled && form.exit_kind === 'landing' && !form.exit && !isSocks) { toast('请选择出口节点', 'error'); return }
    setLoading(true)
    try {
      const payload = { ...form }
      if (isSocks) {
        payload.proto = 'tcp'
        payload.mode = 'userspace'
        payload.exit_type = 'socks5'
        // 用户端不填 CONNECT：用 SK5 URI 的 host:port 占位；代理入口同理。
        if (!String(payload.exit || '').trim()) {
          payload.exit = hostPortFromSocksURI(payload.exit_uri) || payload.exit
        }
      } else if (isPort || form.exit_type !== 'socks5') {
        payload.exit_type = 'direct'
        payload.exit_uri = ''
      }
      if (isPort) payload.via_node_ids = []
      await onSubmit(payload)
    } catch (err) { toast(err.message, 'error') } finally { setLoading(false) }
  }

  // Select 的 label 必须是纯字符串（搜索过滤要 .toLowerCase()）：协议栈、
  // 倍率走文本前缀；单点/组合的区分走 options 的 icon 字段。
  // showStack=false（用户端）时不暴露 v4/v6 能力标签，只显示线路名。
  const fmtStack = (n) => {
    if (showStack === false) return ''
    const { entryV4, entryV6, exitV6 } = nodeStack(n)
    const parts = [entryV4 && 'v4', entryV6 && 'v6'].filter(Boolean)
    let tag = parts.join('+')
    if (exitV6 !== entryV6) {
      const note = exitV6 ? '出口支持v6' : '出口不支持v6'
      tag = tag ? `${tag} ${note}` : note
    }
    return tag ? `[${tag}] ` : ''
  }
  const fmtRate = (n) => {
    const stack = fmtStack(n)
    if (showRate === false) return `${stack}${n.name}`
    const r = n.rate_multiplier ?? 1
    return r !== 1 ? `${stack}${n.name} (×${r})` : `${stack}${n.name}`
  }
  // User side (showStack=false): no single/composite icons — only a plain name.
  const nodeOption = (n) => ({
    value: n.id,
    label: fmtRate(n),
    icon: showStack === false ? null : <NodeTypeIcon type={n.node_type} />,
  })
  // Only entry-role nodes can be the rule's entry — roles missing (nodes
  // reported by a server that predates the roles column) default to entry,
  // so old deployments keep working until an admin explicitly narrows roles.
  //
  // `nodes` is already scoped by the parent:
  //   - 替用户创建 / 用户侧：仅已授权线路
  //   - 管理员全局规则：全部节点
  // 代理 tab 必须落在同一集合内，绝不能用全局 proxy-services 展开未授权节点。
  const entryNodes = nodes.filter(n => ((n.roles ?? 1) & 1) !== 0)
  const allowedNodeIds = useMemo(
    () => new Set((nodes || []).map(n => Number(n.id)).filter(id => id > 0)),
    [nodes],
  )
  // Tabbed groups: 单点 [+ 组合 for chain] + 代理 (proxy-service deployed nodes).
  // Port variant never shows the composite tab; proxy tab still filters composites out for port.
  const singleOpts = entryNodes.filter(n => n.node_type !== 'composite').map(nodeOption)
  const compositeOpts = entryNodes.filter(n => n.node_type === 'composite').map(nodeOption)
  // 代理 tab: 每条覆盖后的代理服务单独一项（同节点多协议并列）；value 仍为 node_id。
  // 只展示「已在 nodes 里」且（若提供了 proxyNodeIds）确实部署了代理服务的节点。
  const entryById = Object.fromEntries(entryNodes.map(n => [Number(n.id), n]))
  const proxyOptsFromServices = []
  for (const s of proxyServices || []) {
    // User-scoped: hide services not in the protocol grant set.
    if (grantedSvcIds && !grantedSvcIds.has(Number(s.id))) continue
    const nids = (s.deployed_node_ids || []).map(Number).filter(id => id > 0)
    for (const nid of nids) {
      // Hard gate: never list a node outside the parent-provided pool.
      if (!allowedNodeIds.has(nid)) continue
      // When proxyNodeIds is supplied, require membership (deployed ∩ allowed).
      if (proxyIds.size > 0 && !proxyIds.has(nid)) continue
      const n = entryById[nid]
      if (!n) continue
      const proto = PROTO_LABEL[s.protocol] || s.protocol || ''
      let label = s.name || n.name || '(未命名)'
      if (n.name && n.name !== s.name) label = `${s.name || '(未命名)'} · ${n.name}`
      if (proto) label = `[${proto}] ${label}`
      const base = nodeOption(n)
      // Unique value per service so Select can list multi-proto on same node;
      // form still stores node_id via onChange remap below.
      const rateSuffix = (showRate !== false && base.label.includes('(×'))
        ? ` ${base.label.slice(base.label.indexOf('(×'))}`
        : ''
      proxyOptsFromServices.push({
        value: `proxy:${s.id}:${nid}`,
        label: `${label}${rateSuffix}`,
        icon: base.icon,
        _nodeId: nid,
      })
    }
  }
  const proxyOpts = proxyOptsFromServices.length
    ? proxyOptsFromServices
    : entryNodes
      .filter(n => {
        const id = Number(n.id)
        if (!allowedNodeIds.has(id)) return false
        // Fallback when /proxy-services unavailable: only nodes known to host proxy.
        return proxyIds.size === 0 ? false : proxyIds.has(id)
      })
      .map(nodeOption)
  // 端口/链式选线路统一：单点 | 组合 | 代理
  const groups = [
    { label: '单点', options: singleOpts },
    { label: '组合', options: compositeOpts },
    { label: '代理', options: proxyOpts },
  ]

  // 落地仓库 socks5 → SK5 代理 URI（优先用仓库 uri，否则按 host/port 拼无认证）。
  const buildRepoSocksURI = (n) => {
    const uri = String(n?.uri || '').trim()
    if (uri && /^socks5?:\/\//i.test(uri)) return uri
    const host = String(n?.host || '').trim()
    const port = Number(n?.port) || 0
    if (!host || !port) return ''
    const hostPart = host.includes(':') && !host.startsWith('[') ? `[${host}]` : host
    return `socks5://${hostPart}:${port}`
  }
  // value 用 repo:<id>，避免同 URI 多节点冲突；选中后写入 URI + CONNECT 目标 host:port。
  const fmtHostPort = (host, port) => {
    const h = String(host || '').trim()
    const p = Number(port) || 0
    if (!h || !p) return ''
    return h.includes(':') && !h.startsWith('[') ? `[${h}]:${p}` : `${h}:${p}`
  }
  const hostPortFromSocksURI = (uri) => {
    const s = String(uri || '').trim()
    if (!s) return ''
    try {
      // URL() 对 socks5:// 可用；user:pass@ 会解析到 hostname/port
      const u = new URL(s)
      if (u.hostname && u.port) return fmtHostPort(u.hostname, u.port)
      if (u.hostname && u.host.includes(':')) {
        // 少数环境 port 为空但 host 带端口
        return u.host
      }
    } catch { /* fall through */ }
    // 兜底：取 @ 后或协议后的 host:port
    const m = s.match(/^(?:socks5?:\/\/)?(?:[^@/]+@)?(\[[^\]]+\]|[^:/?#]+):(\d+)/i)
    if (m) return fmtHostPort(m[1].replace(/^\[|\]$/g, ''), m[2])
    return ''
  }
  const repoSocksByKey = {}
  const repoSocksOptions = []
  for (const n of repoSocksNodes || []) {
    const uri = buildRepoSocksURI(n)
    if (!uri) continue
    const key = `repo:${n.id}`
    const connect = fmtHostPort(n.host, n.port) || hostPortFromSocksURI(uri)
    repoSocksByKey[key] = { uri, connect, name: n.name || '' }
    const proto = String(n.protocol || 'socks5').toLowerCase() || 'socks5'
    const label = `${n.name || '(未命名)'}${n.host ? ` · ${n.host}:${n.port || ''}` : ''}`
    repoSocksOptions.push({ value: key, label: `[${proto}] ${label}` })
  }
  const repoSocksSelectValue = (() => {
    const cur = String(form.exit_uri || '').trim()
    if (!cur) return ''
    for (const [k, meta] of Object.entries(repoSocksByKey)) {
      if (meta.uri === cur) return k
    }
    return ''
  })()
  const applyRepoSocks = (key) => {
    const meta = repoSocksByKey[key]
    if (!meta?.uri) return
    setForm(f => {
      const next = { ...f, exit_uri: meta.uri }
      // 导入时自动填 CONNECT 目标；已有目标则覆盖为该 SK5 节点地址（与仓库一致）
      if (meta.connect) next.exit = meta.connect
      // 名称/备注为空时用仓库节点名轻量预填
      if (!String(f.name || '').trim() && meta.name) next.name = meta.name
      if (!String(f.comment || '').trim() && meta.name) next.comment = meta.name
      return next
    })
  }

  // Show protocol + node remark only — the real connection address is hidden
  // from the picker. The value stays host:port (the rule's exit target).
  const landingOptions = (landingNodes || []).map(n => {
    const label = `${n.protocol ? `[${n.protocol}] ` : ''}${n.name || '(未命名)'}${n.expires_at > 0 ? ` · ${fmtDate(n.expires_at)}` : ''}`
    // Add a colored dot icon based on expiry status
    let icon = null
    if (n.expires_at > 0) {
      const now = Math.floor(Date.now() / 1000)
      const daysLeft = (n.expires_at - now) / 86400
      const color = n.expires_at <= now ? '#9ca3af' : daysLeft > 3 ? '#22c55e' : '#ef4444'
      icon = <span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: '50%', backgroundColor: color, flexShrink: 0 }} />
    }
    return { value: `${n.host}:${n.port}`, label, icon }
  })

  // Cascaded middle-layer picker: chain[i]'s candidates are the binding
  // graph's downstreams of chain[i-1] (chain[-1] = the entry), narrowed to
  // nodes we actually have (the my-side list is already the granted
  // intersection) with the via role, excluding the entry itself and any
  // node already on the chain. Missing roles default to "not a via node"
  // (the opposite default from entry) — an unrolled node shouldn't silently
  // become choosable as a middle hop. Candidates empty and nothing chosen
  // at a level means stop rendering further levels — "有框就有得选".
  const nodeById = Object.fromEntries(nodes.map(n => [n.id, n]))
  const viaChain = (form.via_node_ids || []).map(Number).filter(id => nodeById[id])
  const viaCandidates = (upstreamId, chainSoFar) =>
    bindings
      .filter(b => b.upstream_node_id === upstreamId)
      .map(b => nodeById[b.downstream_node_id])
      .filter(n => n && ((n.roles ?? 0) & 2) !== 0)
      .filter(n => n.id !== Number(form.node_id) && !chainSoFar.includes(n.id))
  const pickVia = (level, v) => setForm(f => {
    const next = (f.via_node_ids || []).slice(0, level)
    if (v) next.push(Number(v))
    return { ...f, via_node_ids: next }
  })
  const viaLevels = []
  if (form.node_id) {
    let upstream = Number(form.node_id)
    const soFar = []
    for (let level = 0; ; level++) {
      const chosen = viaChain[level]
      const cands = viaCandidates(upstream, soFar)
      if (!cands.length && !chosen) break
      // A no_direct_exit upstream can't terminate the chain here, so this
      // level loses its 直接转发 option and must pick a layer.
      viaLevels.push({ level, cands, chosen, mustVia: !!nodeById[upstream]?.no_direct_exit })
      if (!chosen) break
      soFar.push(chosen)
      upstream = chosen
    }
  }
  // Exit-capability hints follow the chain tail (the last via, or the entry
  // itself when the chain is empty) — the tail is the node that actually
  // dials the target, so its stack is what the outbound leg depends on.
  // The exit probe originates from the tail for the same reason: probing
  // entry → target would test a path the traffic never takes. A composite
  // tail is resolved to its own last hop server-side.
  const tailNode = viaChain.length ? nodeById[viaChain[viaChain.length - 1]] : nodeById[Number(form.node_id)]
  // The tail launches the exit segment, which a no_direct_exit node may never
  // do. The server enforces the same rule when deriving the chain; blocking
  // submit here just surfaces the error before the round trip.
  const tailNoDirect = !!tailNode?.no_direct_exit

  // Flatten a chain node into physical member names for display: a composite is
  // shown as its member nodes (the reference is what's stored/selected; the
  // preview unpacks it), a single node as itself. Composite children come from
  // the node payload's resolved `hops`.
  const flattenNode = (id) => {
    const n = nodeById[Number(id)]
    if (!n) return []
    if (n.node_type === 'composite' && n.hops?.length) {
      return n.hops.map(h => h.name || `#${h.node_id}`)
    }
    return [n.name]
  }
  const flatChainNames = form.node_id
    ? [...flattenNode(form.node_id), ...viaChain.flatMap(flattenNode)]
    : []

  return (
    <Modal open={open} onClose={onClose} title={title}>
      <form onSubmit={submit} className="space-y-[22px]">
        {allowTypeSwitch && (
          <div className="grid grid-cols-[120px_1fr] gap-6 items-center">
            <label className="fl">规则类型</label>
            <div className="flex items-center gap-1.5 flex-wrap">
              {[['port', '端口转发'], ['chain', '链式转发']].map(([key, label]) => (
                <button key={key} type="button" onClick={() => switchLocalVariant(key)}
                  className={`chip-btn ${localVariant === key ? 'is-active' : ''}`}>{label}</button>
              ))}
            </div>
          </div>
        )}
        <div className="grid grid-cols-[120px_1fr] gap-6 items-center">
          <label className="fl">选择线路</label>
          <Select
            value={entrySelectValue}
            onChange={pickEntry}
            placeholder="-- 选择节点 --"
            searchable
            tabs
            groups={groups}
            options={undefined}
          />
          {!isPort && viaLevels.map(({ level, cands, chosen, mustVia }) => (
            <Fragment key={level}>
              <label className="fl">{level === 0 ? '线路层' : `线路层 ${level + 1}`}</label>
              <Select value={chosen ?? ''} onChange={v => pickVia(level, v)} placeholder={mustVia ? '-- 选择线路层 --' : '直接转发'}
                options={[...(mustVia ? [] : [{ value: '', label: '直接转发' }]), ...cands.map(nodeOption)]} />
            </Fragment>
          ))}
          {!isPort && tailNoDirect && (
            <>
              <label className="fl"></label>
              <div className="text-xs text-red-600">
                {viaLevels.length && !viaLevels[viaLevels.length - 1].chosen
                  ? `节点「${tailNode.name}」禁止直接转发，必须选择线路层。`
                  : `节点「${tailNode.name}」禁止直接转发，但没有可级联的线路层，无法保存该链路。`}
              </div>
            </>
          )}
          {!isPort && viaChain.length > 0 && (
            <>
              <label className="fl"></label>
              <div className="text-xs text-ink-mut">
                <span className="font-mono">
                  {[...flatChainNames, '目标'].filter(Boolean).join(' → ')}
                </span>
                <span className="ml-2">链路更长的规则占用更多全局转发名额</span>
              </div>
            </>
          )}
          {users && users.length > 0 && (
            <>
              <label className="fl">所属用户</label>
              <Select value={form.owner_id} onChange={v => set('owner_id', Number(v))} placeholder="-- 选择用户 --" searchable
                options={users.map(u => ({ value: u.id, label: u.username }))} />
            </>
          )}
          <label className="fl">名称</label>
          <input className="input-field" value={form.name} onChange={e => set('name', e.target.value)} required placeholder="规则名称" />
          <label className="fl">协议</label>
          <Select
            value={isSocks ? 'tcp' : form.proto}
            onChange={v => set('proto', v)}
            style={{ maxWidth: 200 }}
            disabled={isSocks}
            options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }, { value: 'tcp+udp', label: 'TCP+UDP' }]}
          />
          {/* 模式作用于出口段（最后一跳 → 目标）：单点规则即唯一一跳；
              组合链路的节点间各跳模式由组合节点配置决定，这里只管出口段 */}
          {(() => {
            const selNode = nodes.find(n => String(n.id) === String(form.node_id))
            if (!selNode) return null
            // The mode field governs the outbound leg (tail → target): a
            // composite entry already has that split internally, and a via
            // chain extends it the same way, so either makes this the
            // "出口段" (vs. the whole rule) regardless of the entry's own type.
            const composite = tailNode?.node_type === 'composite' || viaChain.length > 0
            if (isSocks) {
              return (
                <>
                  <label className="fl">{composite ? '出口段模式' : '转发模式'}</label>
                  <div className="flex items-center gap-2.5 flex-wrap">
                    <Select value="userspace" disabled style={{ width: 160 }}
                      options={[{ value: 'userspace', label: '用户态' }]} />
                    <span className="text-xs text-ink-mut">
                      {Number(form.proxy_service_id) > 0
                        ? '代理入口：核心 inbound 走入口协议；出站为开放 SOCKS（3x-ui 链式，非 nft 直转）'
                        : 'SOCKS5 出口强制用户态 TCP（末跳经 SK5 CONNECT 到目标）'}
                    </span>
                  </div>
                </>
              )
            }
            return (
              <>
                <label className="fl">{composite ? '出口段模式' : '转发模式'}</label>
                <div className="flex items-center gap-2.5 flex-wrap">
                  <Select value={form.mode || 'kernel'} onChange={v => set('mode', v)} style={{ width: 160 }}
                    options={[{ value: 'kernel', label: '内核态' }, { value: 'userspace', label: '用户态' }]} />
                  <span className="text-xs text-ink-mut">
                    {composite
                      ? '仅作用于最后一跳 → 目标。线路稳定、低丢包时用内核态（性能更好）；跨境或网络不稳定、丢包高时用用户态（更抗抖动）。用户态仅 TCP，UDP 自动走内核态'
                      : '线路稳定、低丢包时用内核态（性能更好，支持 TCP/UDP）；跨境或网络不稳定、丢包高时用用户态（更抗抖动，仅 TCP，UDP 自动走内核态）'}
                  </span>
                </div>
              </>
            )
          })()}
          <label className="fl">端口设置 <span className="text-ink-mut font-normal text-xs">(可选)</span></label>
          <input className="input-field font-mono" type="number" min="1" max="65535" value={form.entry_port} onChange={e => set('entry_port', e.target.value)}
            placeholder="留空自动分配" style={{ maxWidth: 200 }} />

          {isChain && (
            <>
              <label className="fl">出口类型</label>
              <Select
                value={form.exit_type || 'direct'}
                onChange={v => setForm(f => ({
                  ...f,
                  exit_type: v,
                  proto: v === 'socks5' ? 'tcp' : f.proto,
                  mode: v === 'socks5' ? 'userspace' : f.mode,
                  exit_kind: v === 'socks5' ? 'custom' : f.exit_kind,
                }))}
                style={{ maxWidth: 220 }}
                options={[
                  { value: 'direct', label: '直连 host:port' },
                  { value: 'socks5', label: 'SOCKS5 代理' },
                ]}
              />
            </>
          )}

          {isSocks ? (
            <>
              <label className="fl">SK5 代理</label>
              <div className="space-y-2 min-w-0">
                <div className="flex items-start gap-3">
                  <div className="flex-1 min-w-0 space-y-2">
                    <Select
                      value={repoSocksSelectValue}
                      onChange={v => applyRepoSocks(v)}
                      placeholder={repoSocksOptions.length
                        ? (showStack === false ? '-- 选择 SOCKS5 落地 --' : '-- 从落地仓库导入 --')
                        : (showStack === false ? '暂无可用 SOCKS5 落地' : '落地仓库暂无 SOCKS5')}
                      searchable={repoSocksOptions.length > 0}
                      disabled={repoSocksOptions.length === 0}
                      options={repoSocksOptions.length
                        ? repoSocksOptions
                        : [{ value: '', label: showStack === false ? '暂无 SOCKS5 落地，请联系管理员分配' : '暂无 SOCKS5 节点，请先在落地仓库添加' }]}
                    />
                    {/* 用户端禁止手填 SK5 URI；管理员保留导入/手填 */}
                    {showStack !== false && (
                      <input
                        className="input-field font-mono"
                        value={form.exit_uri || ''}
                        onChange={e => set('exit_uri', e.target.value)}
                        onBlur={() => {
                          const uri = String(form.exit_uri || '').trim()
                          if (!uri || String(form.exit || '').trim()) return
                          const hp = hostPortFromSocksURI(uri)
                          if (hp) set('exit', hp)
                        }}
                        required
                        placeholder="socks5://user:pass@proxy:1080（可导入或手填）"
                      />
                    )}
                    <div className="text-[11px] text-ink-mut">
                      {showStack === false
                        ? (repoSocksOptions.length
                          ? `请从列表选择 SOCKS5 节点（${repoSocksOptions.length} 个）`
                          : '暂无可用 SOCKS5 节点，请联系管理员在落地中分配')
                        : (repoSocksOptions.length
                          ? `可从落地仓库导入（${repoSocksOptions.length} 个 SOCKS5），也可手动填写 URI`
                          : '落地仓库暂无 SOCKS5 节点：请到「落地仓库 → 添加节点 → SOCKS5 快捷」添加后再导入')}
                    </div>
                  </div>
                  {/* 用户端：测试挂在 SK5 选择旁；管理员仍在 CONNECT 行 */}
                  {showStack === false && (
                    <ProbeButton
                      target={form.exit || hostPortFromSocksURI(form.exit_uri)}
                      nodeId={tailNode?.id ?? form.node_id}
                      disabled={!form.node_id || !(form.exit || hostPortFromSocksURI(form.exit_uri))}
                      disabledTitle={!form.node_id ? '请先选择线路' : '请先选择 SOCKS5 节点'}
                    />
                  )}
                </div>
              </div>
              {showStack !== false && (
                <>
                  <label className="fl">
                    CONNECT 目标
                    {Number(form.proxy_service_id) > 0 && (
                      <span className="text-ink-mut font-normal text-xs ml-1">(开放代理可留空)</span>
                    )}
                  </label>
                  <div className="flex items-center gap-3">
                    <input
                      className="input-field font-mono flex-1"
                      value={form.exit}
                      onChange={e => set('exit', e.target.value)}
                      required={!(Number(form.proxy_service_id) > 0)}
                      placeholder={Number(form.proxy_service_id) > 0
                        ? '开放代理：可留空（客户端目标经 SK5 透传）'
                        : 'host:port（业务目标）'}
                    />
                    <ProbeButton
                      target={form.exit}
                      nodeId={tailNode?.id ?? form.node_id}
                      disabled={!form.node_id || !form.exit}
                      disabledTitle={!form.node_id ? '请先选择线路' : '请先填写目标 host:port'}
                    />
                  </div>
                  <label className="fl"></label>
                  <div className="text-xs text-ink-mut">
                    {Number(form.proxy_service_id) > 0
                      ? '已选「代理」入口（对齐 3x-ui）：入口协议入站 + SK5 开放出站（客户端目标透传）。CONNECT 可留空。'
                      : '可从落地仓库导入 SOCKS5 节点，或手动填写 URI。末跳用户态先连 SK5，再对目标做 SOCKS CONNECT。仅 TCP。'}
                  </div>
                </>
              )}
            </>
          ) : landingEnabled ? (
            <>
              <label className="fl">落地IP</label>
              <div className="flex items-center gap-3">
                {landingOptions.length ? (
                  <Select value={form.exit} onChange={v => {
                    const node = (landingNodes || []).find(n => `${n.host}:${n.port}` === v)
                    const p = String(node?.protocol || node?.uri || '').toLowerCase()
                    const mieru = p.includes('mieru')
                    setForm(f => ({
                      ...f,
                      exit: v,
                      exit_kind: 'landing',
                      // 落地分享链写入 exit_uri，协议入口出站用（VLESS→SS 等）
                      exit_uri: node?.uri || f.exit_uri || '',
                      // Official mieru is TCP+UDP; default TCP-only looks
                      // green on panel TCP probe but clients miss UDP.
                      proto: mieru && f.proto === 'tcp' ? 'tcp+udp' : f.proto,
                    }))
                  }} placeholder="-- 选择落地IP --" searchable options={landingOptions} className="flex-1" />
                ) : (
                  <div className="text-xs text-ink-mut flex-1">尚无可用落地IP，请联系管理员分配落地节点或订阅来源。</div>
                )}
                <ProbeButton
                  target={form.exit}
                  nodeId={tailNode?.id ?? form.node_id}
                  disabled={!form.node_id || !form.exit}
                  disabledTitle={!form.node_id ? '请先选择线路' : '请先选择落地IP'}
                />
              </div>
              {Number(form.proxy_service_id) > 0 && (
                <>
                  <label className="fl"></label>
                  <div className="text-xs text-ink-mut">
                    已选「代理」入口：出站按落地协议（SS/VLESS…）转发（对齐 3x-ui），不是裸 TCP 隧道。
                  </div>
                </>
              )}
            </>
          ) : (
            <>
              <label className="fl">落地IP</label>
              <div className="flex items-center gap-3">
                <input className="input-field font-mono flex-1" value={form.exit} onChange={e => set('exit', e.target.value)} required placeholder="host:port" />
                <ProbeButton
                  target={form.exit}
                  nodeId={tailNode?.id ?? form.node_id}
                  disabled={!form.node_id || !form.exit}
                  disabledTitle={!form.node_id ? '请先选择线路' : '请先填写落地 host:port'}
                />
              </div>
            </>
          )}

          <label className="fl">备注 <span className="text-ink-mut font-normal text-xs">(可选)</span></label>
          <input className="input-field" value={form.comment} onChange={e => set('comment', e.target.value)} placeholder="备注" />
        </div>
        <div className="flex items-center gap-3 pt-4 border-t border-line-soft">
          <button type="submit" disabled={loading} className="btn-primary">{submitLabel}</button>
          <button type="button" onClick={onClose} className="btn-secondary">取消</button>
        </div>
      </form>
    </Modal>
  )
}

/* Map a rule into the form's editable fields. The rule-list row carries split
   exit_host/exit_port; the detail view a combined exit string — accept either.
   exit_kind comes from the (possibly client-enriched) rule so edit prefills the
   right exit mode. */
export function ruleToForm(rule) {
  const exit = rule.exit != null ? rule.exit
    : (rule.exit_host && rule.exit_port ? `${rule.exit_host}:${rule.exit_port}` : '')
  const exitType = rule.exit_type === 'socks5' ? 'socks5' : 'direct'
  return {
    node_id: rule.node_id,
    owner_id: rule.owner_id?.Valid ? rule.owner_id.Int64 : (rule.owner_id || 0),
    name: rule.name,
    proto: rule.proto,
    exit,
    exit_kind: rule.exit_kind === 'landing' ? 'landing' : 'custom',
    exit_type: exitType,
    // List API redacts password; edit may need re-paste of full URI.
    exit_uri: rule.exit_uri || '',
    entry_port: rule.entry_listen_port > 0 ? String(rule.entry_listen_port) : '',
    comment: rule.comment || '',
    // 模式字段的语义是出口段（尾跳）；entry_mode 兜底兼容旧列表载荷，
    // 单点规则两者本就相同。
    mode: rule.exit_mode || rule.entry_mode || 'kernel',
    via_node_ids: rule.via_node_ids || [],
    proxy_service_id: Number(rule.proxy_service_id) || 0,
  }
}

/* Prefill for "copy": same chain/target, name suffixed _Copy. entry_port stays
   blank — the source rule still holds its port, so the copy needs a fresh one. */
export function copyInitial(rule) {
  return { ...ruleToForm(rule), name: `${rule.name}_Copy`, entry_port: '' }
}

/* Map the form's fields to the create/edit request body — the single source
   of the payload shape for every rules page, so a new field can't silently
   go missing from one of the call sites. form.mode is the exit-segment mode;
   it is sent as both exit_mode (the real field) and mode (legacy alias for
   single-node rules, so older servers keep honoring it). via_node_ids is
   always sent (never omitted): the form owns the whole chain, so an empty
   array means "clear the chain", not "leave it untouched". entry_family is not
   sent: the server derives it from the entry node's relay addresses. */
export function ruleFormToPayload(form) {
  const exitType = form.exit_type === 'socks5' ? 'socks5' : 'direct'
  const payload = {
    node_id: Number(form.node_id), name: form.name, proto: form.proto,
    mode: form.mode || undefined, exit_mode: form.mode || undefined,
    exit: form.exit, entry_port: form.entry_port ? Number(form.entry_port) : undefined,
    comment: form.comment || undefined,
    via_node_ids: (form.via_node_ids || []).map(Number),
    owner_id: form.owner_id ? Number(form.owner_id) : undefined,
    exit_type: exitType,
    // 0 clears a previous 代理-tab pick when re-saving as plain node.
    proxy_service_id: Number(form.proxy_service_id) || 0,
  }
  if (exitType === 'socks5') {
    payload.exit_uri = form.exit_uri || ''
    payload.proto = 'tcp'
    payload.mode = 'userspace'
    payload.exit_mode = 'userspace'
  } else {
    // Direct + landing share (ss:// / vless:// …) for protocol-entry egress.
    // Bare host:port alone cannot speak SS — credentials go in exit_uri.
    // List API redacts as ss://***@host:port — never re-submit that (server
    // would store garbage and SS outbound parse fails). Prefer landing re-pick.
    const eu = String(form.exit_uri || '').trim()
    const redacted = !eu || eu.includes(':***@') || eu.includes('://***@') || eu.includes('://***')
    if (!redacted && /^(ss|shadowsocks|vless|vmess|trojan|socks5?|hy2|hysteria2|mieru|mierus):\/\//i.test(eu)) {
      payload.exit_uri = eu
    } else {
      // Empty: server keeps prior secret on edit when body omits usable uri;
      // create/update with empty + host:port still resolves from warehouse.
      payload.exit_uri = ''
    }
  }
  return payload
}

/** Port vs chain classification for list tabs. */
export function isChainRule(rule, nodeMap = {}) {
  if (rule?.via_node_ids?.length) return true
  if (rule?.exit_type === 'socks5') return true
  const n = nodeMap[rule?.node_id]
  return n?.node_type === 'composite'
}
