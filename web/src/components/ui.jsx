import { useState, useRef, useEffect, useCallback, useMemo, createContext, useContext } from 'react'
import { createPortal } from 'react-dom'
import { copyToClipboard } from '../lib/clipboard'

/* ---------- Modal ---------- */
// Portal to document.body so nested page overflow/transform can't clip the sheet.
// Animation is opacity-only: any transform/filter on the panel creates a containing
// block that crops native <input type="date"> calendars and Select portals.
export function Modal({ open, onClose, title, children, wide }) {
  if (!open) return null
  return createPortal(
    <div
      className="fixed inset-0 z-[80] flex items-start sm:items-center justify-center bg-black/50 backdrop-blur-[4px] px-4 py-6 overflow-y-auto"
      onClick={onClose}
    >
      <div
        className={`relative z-[81] bg-surface border border-line rounded-[20px] shadow-[0_28px_80px_-24px_rgba(15,23,42,0.55)] w-full my-auto ${wide ? 'max-w-3xl' : 'max-w-xl'} animate-modal-in`}
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-6 py-4 border-b border-line-soft">
          <h3 className="text-[16px] font-bold tracking-tight text-ink">{title}</h3>
          <button type="button" onClick={onClose} className="w-8 h-8 rounded-lg text-ink-mut hover:text-ink hover:bg-raised transition-colors grid place-items-center text-lg leading-none" aria-label="关闭弹窗">&times;</button>
        </div>
        <div className="px-6 py-6">{children}</div>
      </div>
    </div>,
    document.body,
  )
}

/* ---------- Confirm ---------- */
export function Confirm({ open, onClose, onConfirm, title, children }) {
  return (
    <Modal open={open} onClose={onClose} title={title || '确认操作'}>
      <div className="text-sm text-ink-soft mb-5">{children}</div>
      <div className="flex gap-3 justify-end">
        <button onClick={onClose} className="btn-secondary">取消</button>
        <button onClick={onConfirm} className="btn-danger">确认</button>
      </div>
    </Modal>
  )
}

/* ---------- Badge ---------- */
const badgeColors = {
  green: 'bg-emerald-500/[.14] text-emerald-700 dark:text-emerald-300 border-emerald-500/35',
  amber: 'bg-amber-500/[.14] text-amber-700 dark:text-amber-300 border-amber-500/35',
  red: 'bg-rose-500/[.14] text-rose-700 dark:text-rose-300 border-rose-500/35',
  gray: 'bg-raised text-ink-soft border-line',
  blue: 'bg-sky-500/[.14] text-sky-700 dark:text-sky-300 border-sky-500/35',
  violet: 'bg-violet-500/[.14] text-violet-700 dark:text-violet-300 border-violet-500/35',
  teal: 'bg-teal-500/[.14] text-teal-700 dark:text-teal-300 border-teal-500/35',
  cyan: 'bg-cyan-500/[.14] text-cyan-700 dark:text-cyan-300 border-cyan-500/35',
}
export function Badge({ color = 'gray', className = '', children, ...rest }) {
  // Forward ...rest (notably title) so hovering a status badge can reveal detail
  // like a node's last_error — previously these props were silently dropped.
  return <span {...rest} className={`inline-flex items-center gap-1.5 px-2.5 py-[3px] rounded-full text-[11.5px] font-semibold border backdrop-blur-[2px] ${badgeColors[color] || badgeColors.gray} ${className}`}>{children}</span>
}

/* ---------- NodeTypeBadge ---------- */
// Single = a solid node dot; composite = interlocking chain links (a multi-hop
// chain); self = the panel host itself.
const nodeTypeIcon = {
  single: <span className="w-1.5 h-1.5 rounded-full bg-green-500 flex-none" />,
  composite: (
    <svg className="w-[13px] h-[13px] flex-none" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M9 17H7A5 5 0 0 1 7 7h2" /><path d="M15 7h2a5 5 0 0 1 0 10h-2" /><path d="M8 12h8" />
    </svg>
  ),
  self: (
    <svg className="w-[13px] h-[13px] flex-none" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="m3 9 9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" /><path d="M9 22V12h6v10" />
    </svg>
  ),
}
export function NodeTypeBadge({ type }) {
  if (type === 'composite') return <Badge color="violet">{nodeTypeIcon.composite}组合</Badge>
  if (type === 'self') return <Badge color="blue">{nodeTypeIcon.self}自身</Badge>
  return <Badge color="green">{nodeTypeIcon.single}单点</Badge>
}

// Bare colored glyph for cramped spots like table cells: the same icons as
// NodeTypeBadge without the pill, so a rule row can flag single vs composite
// at a glance without widening the column.
export function NodeTypeIcon({ type }) {
  if (!type) return null
  if (type === 'composite') return <span className="inline-flex text-violet-500 flex-none" title="组合节点">{nodeTypeIcon.composite}</span>
  if (type === 'self') return <span className="inline-flex text-emerald-500 flex-none" title="面板自身">{nodeTypeIcon.self}</span>
  return <span className="inline-flex flex-none" title="单点节点">{nodeTypeIcon.single}</span>
}

/* ---------- NodeStackBadge ---------- */
// 组合节点的入口可达地址来自首跳，出口 IPv6 转发能力来自尾跳（与
// RegenerateRule 里 exitIsIPv6 校验最后一跳 relay_host_v6 的语义一致）；
// 单点节点入口出口就是它自己，两者天然相等。
export function nodeStack(node) {
  const entryV4 = node.node_type === 'composite' ? !!node.entry_relay_host : !!node.relay_host
  const entryV6 = node.node_type === 'composite' ? !!node.entry_relay_host_v6 : !!node.relay_host_v6
  const exitV6 = node.node_type === 'composite' ? !!node.exit_relay_host_v6 : !!node.relay_host_v6
  return { entryV4, entryV6, exitV6 }
}

export function NodeStackBadge({ node }) {
  const { entryV4, entryV6, exitV6 } = nodeStack(node)
  const entryParts = [entryV4 && 'v4', entryV6 && 'v6'].filter(Boolean)
  if (entryParts.length === 0 && exitV6 === entryV6) return null
  return (
    <span className="inline-flex items-center gap-1">
      {entryParts.length > 0 && <Badge color="gray">{entryParts.join('+')}</Badge>}
      {exitV6 !== entryV6 && <Badge color={exitV6 ? 'blue' : 'amber'}>出口{exitV6 ? '支持' : '不支持'} v6</Badge>}
    </span>
  )
}

/* ---------- NodeBillingBadges ---------- */
// Admin list / dashboard: surface non-default billing so operators don't
// need to open each node detail. Multiplier=1 and bidirectional stay silent
// to avoid noise on the common case.
export function NodeBillingBadges({ node, className = '' }) {
  if (!node) return null
  const mult = Number(node.rate_multiplier)
  const showMult = Number.isFinite(mult) && mult > 0 && mult !== 1
  const uni = !!node.unidirectional
  if (!showMult && !uni) return null
  return (
    <span className={`inline-flex items-center gap-1 ${className}`}>
      {showMult && <Badge color="blue" title="计费倍率">×{mult % 1 === 0 ? mult.toFixed(0) : mult}</Badge>}
      {uni && <Badge color="amber" title="单向计费：只计出站">单向</Badge>}
    </span>
  )
}

/* ---------- ExitKindBadge ---------- */
// Distinguishes a landing-node exit (resolved from the user's subscription)
// from a custom host:port exit. Landing exits show the concrete protocol
// (vless/ss/snell/...) rather than a generic label, since that's what the
// user actually needs to know about the exit; falls back to "落地" for
// older data with no recorded protocol.
export function ExitKindBadge({ kind, protocol }) {
  if (kind === 'landing') return <Badge color="blue">{protocol || '落地'}</Badge>
  return <Badge color="gray">自定义</Badge>
}

/* ---------- ProtoBadge ---------- */
export function ProtoBadge({ proto }) {
  if (!proto) return null
  const p = proto.toLowerCase()
  if (p === 'tcp') return <span className="tag-tcp">TCP</span>
  if (p === 'udp') return <span className="tag-udp">UDP</span>
  return <span className="tag-tcpudp">TCP+UDP</span>
}

/* ---------- ModeBadge ---------- */
export function ModeBadge({ mode }) {
  if (!mode) return null
  if (mode === 'kernel') return <span className="tag-kernel">kernel</span>
  return <span className="tag-user">userspace</span>
}

/* ---------- Table ---------- */
export function Table({ children }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        {children}
      </table>
    </div>
  )
}

/* ---------- Empty ---------- */
export function Empty({ title, desc, children }) {
  return (
    <div className="py-12 text-center text-ink-mut">
      <div className="w-11 h-11 rounded-xl bg-raised border border-line mx-auto mb-3 flex items-center justify-center">
        <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M12 8v4M12 16h.01"/></svg>
      </div>
      {title && <h4 className="text-[13.5px] font-semibold text-ink-soft mb-1">{title}</h4>}
      {desc && <p className="text-[12.5px] max-w-sm mx-auto leading-relaxed">{desc}</p>}
      {children && <div className="mt-4 flex items-center justify-center gap-2 flex-wrap">{children}</div>}
    </div>
  )
}

/* ---------- ErrorState: load failure with retry ---------- */
export function ErrorState({ title = '加载失败', desc, onRetry }) {
  return (
    <Empty title={title} desc={desc || '请检查网络后重试。'}>
      {onRetry && (
        <button type="button" onClick={onRetry} className="btn-secondary text-xs">重试</button>
      )}
    </Empty>
  )
}

/* ---------- Loading ---------- */
export function Loading() {
  return (
    <div className="flex items-center justify-center py-20">
      <div className="w-6 h-6 border-2 border-emerald-600 border-t-transparent rounded-full animate-spin" />
    </div>
  )
}

/* ---------- Spinner (inline) ---------- */
export function Spinner({ className = 'w-4 h-4' }) {
  return <div className={`${className} border-2 border-current border-t-transparent rounded-full animate-spin`} />
}

/* ---------- CopyText ---------- */
export function CopyText({ text, children }) {
  const [copied, setCopied] = useState(false)
  const copy = (e) => {
    e.stopPropagation()
    copyToClipboard(text).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1200)
    }).catch(() => { /* text stays on screen for manual copy; no toast here */ })
  }
  return (
    <span className="cursor-pointer relative inline-flex items-center gap-1 group" onClick={copy} title="点击复制">
      {children || text}
      <svg className="w-3.5 h-3.5 opacity-30 group-hover:opacity-70 transition-opacity flex-none" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
      {copied && (
        <span className="absolute -top-7 left-1/2 -translate-x-1/2 bg-gray-900 text-white text-[11px] px-2 py-0.5 rounded whitespace-nowrap pointer-events-none animate-fadeout">
          已复制
        </span>
      )}
    </span>
  )
}

/* ---------- Tooltip ---------- */
/* Hover/focus bubble for cramped spots like table cells. The bubble is
   position:fixed (placed from the trigger's rect) so the Panel's
   overflow-hidden can't clip it — the same escape hatch Modal relies on; a
   plain absolute bubble would be cut off at the panel edge. bg-ink/text-surface
   invert together with the theme, so it reads as a dark bubble in light mode
   and a light one in dark mode. */
export function Tooltip({ content, children, className = '' }) {
  const [box, setBox] = useState(null)
  const ref = useRef(null)
  if (!content) return children
  const show = () => {
    const r = ref.current?.getBoundingClientRect()
    if (r) setBox({ x: r.left + r.width / 2, y: r.top })
  }
  const hide = () => setBox(null)
  return (
    <span ref={ref} className={className} tabIndex={0}
      onMouseEnter={show} onMouseLeave={hide} onFocus={show} onBlur={hide}>
      {children}
      {box && (
        <span role="tooltip" style={{ position: 'fixed', left: box.x, top: box.y }}
          className="z-50 -translate-x-1/2 -translate-y-full -mt-2 max-w-xs px-2.5 py-1.5 rounded-md text-[12px] font-normal leading-snug text-surface bg-ink shadow-lg whitespace-pre-wrap break-words pointer-events-none">
          {content}
        </span>
      )}
    </span>
  )
}

/* ---------- SensText: blurrable sensitive text ---------- */
export function SensText({ children, blurred }) {
  return (
    <span className={blurred ? 'blur-sm hover:blur-[2px] transition-all select-none' : ''}>
      {children}
    </span>
  )
}

/* ---------- ProbeButton ---------- */
// Exit-leg probe from a node (or chain tail). Always render the control even when
// target/node aren't ready yet — callers pass disabled + disabledTitle so the
// affordance stays visible in create/edit forms instead of vanishing.
export function ProbeButton({ target, nodeId, disabled = false, disabledTitle = '' }) {
  const [state, setState] = useState('idle') // idle | loading | ok | fail
  const [result, setResult] = useState('')
  const probe = () => {
    if (disabled || !target) return
    setState('loading')
    let url = `/api/probe?target=${encodeURIComponent(target)}`
    if (nodeId) url += `&node=${nodeId}`
    fetch(url).then(r => r.json()).then(d => {
      if (d.ok) { setState('ok'); setResult(d.latency_ms + 'ms') }
      else { setState('fail'); setResult('不通') }
    }).catch(() => { setState('fail'); setResult('请求失败') })
  }
  const busy = state === 'loading'
  return (
    <span className="inline-flex items-center gap-2">
      <button type="button" onClick={probe} disabled={disabled || busy}
        title={disabled ? (disabledTitle || '请先选择入口与出口') : undefined}
        className="text-[11px] px-2 py-0.5 rounded border border-line bg-surface text-ink-soft hover:border-emerald-500 hover:text-emerald-600 disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:border-line disabled:hover:text-ink-soft">
        {busy ? <Spinner className="w-3 h-3" /> : '测试'}
      </button>
      {state === 'ok' && <span className="text-[11px] text-green-700 font-semibold">{result}</span>}
      {state === 'fail' && <span className="text-[11px] text-red-600">{result}</span>}
      {busy && <span className="text-[11px] text-ink-mut">测试中...</span>}
    </span>
  )
}

/* ---------- ProbeChainButton ---------- */
// Full-path probe for a saved rule (or explicit chain id). Optional probeAllTrigger
// re-runs when the parent bumps a counter (「测试全部」); run optional limit() to
// cap concurrency so N rules don't each fan out hops at once.
export function ProbeChainButton({ chainId, ruleId, probeAllTrigger, limit }) {
  const [state, setState] = useState('idle')
  const [result, setResult] = useState('')
  const probe = () => {
    setState('loading')
    const param = ruleId ? `rule_id=${ruleId}` : `chain=${chainId}`
    const run = () => fetch(`/api/probe-chain?${param}`).then(r => r.json())
    const p = typeof limit === 'function' ? limit(run) : run()
    p.then(d => {
      if (d.hops && d.hops.length) {
        const parts = d.hops.map(h => h.error ? 'x' : h.latency_ms + 'ms')
        const joined = parts.join(' + ')
        if (d.ok) {
          setState('ok')
          setResult(d.hops.length > 1 ? joined + ' = ' + d.latency_ms + 'ms' : d.latency_ms + 'ms')
        } else {
          setState('fail')
          setResult(joined)
        }
      } else if (d.ok) {
        setState('ok'); setResult(d.latency_ms + 'ms')
      } else {
        setState('fail'); setResult(d.error || '不通')
      }
    }).catch(() => { setState('fail'); setResult('请求失败') })
  }
  useEffect(() => {
    if (probeAllTrigger) probe()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [probeAllTrigger])
  const busy = state === 'loading'
  return (
    <span className="inline-flex items-center gap-2">
      <button type="button" onClick={probe} disabled={busy}
        className="text-[11px] px-2 py-0.5 rounded border border-line bg-surface text-ink-soft hover:border-emerald-500 hover:text-emerald-600 disabled:opacity-50">
        {busy ? <Spinner className="w-3 h-3" /> : '测试'}
      </button>
      {state === 'ok' && <span className="text-[11px] text-green-700 font-semibold">{result}</span>}
      {state === 'fail' && <span className="text-[11px] text-red-600">{result}</span>}
      {busy && <span className="text-[11px] text-ink-mut">测试中...</span>}
    </span>
  )
}

/* ---------- Promise-based confirm dialog ---------- */
// useConfirm() returns confirm(opts) -> Promise<boolean>, so callers can write
// `if (!(await confirm({...}))) return` instead of the native window.confirm.
const ConfirmCtx = createContext(() => Promise.resolve(false))
export function useConfirm() { return useContext(ConfirmCtx) }

export function ConfirmProvider({ children }) {
  const [state, setState] = useState(null)
  const confirm = useCallback((opts = {}) => new Promise(resolve => {
    setState({ resolve, ...opts })
  }), [])
  const finish = (result) => setState(s => { s?.resolve(result); return null })
  return (
    <ConfirmCtx.Provider value={confirm}>
      {children}
      <Modal open={!!state} onClose={() => finish(false)} title={state?.title || '确认操作'}>
        <div className="text-sm text-ink-soft mb-5 whitespace-pre-line">{state?.message}</div>
        <div className="flex gap-3 justify-end">
          <button onClick={() => finish(false)} className="btn-secondary">{state?.cancelText || '取消'}</button>
          <button onClick={() => finish(true)} className={state?.danger ? 'btn-danger' : 'btn-primary'}>{state?.confirmText || '确认'}</button>
        </div>
      </Modal>
    </ConfirmCtx.Provider>
  )
}

/* ---------- Select: styled dropdown replacing native <select> ---------- */
// options: [{ value, label, icon? }]. label must stay a plain string — search
// filters on it — so any glyph (e.g. a node-type marker) goes in the optional
// icon field, rendered before the label in menu items and in the single-select
// trigger. Single-select (default): value is a scalar, onChange receives the
// chosen value as a string and the menu closes. Multi-select (multiple=true):
// value is an array, onChange receives the next array of string values and the
// menu stays open so several can be picked.
export function Select({ value, onChange, options = [], groups, placeholder = '请选择', disabled, className = '', style, searchable = false, multiple = false, tabs = false }) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [activeTab, setActiveTab] = useState(0)
  // Fixed-position menu box: absolute menus get clipped by .card/.detail-panel
  // overflow:hidden (e.g. user grant node picker), so place from the trigger rect.
  const [menuBox, setMenuBox] = useState(null)
  const ref = useRef(null)
  const menuRef = useRef(null)
  const triggerRef = useRef(null)

  const placeMenu = useCallback(() => {
    const r = triggerRef.current?.getBoundingClientRect()
    if (!r) return
    const gap = 6
    const maxH = Math.min(320, Math.max(160, window.innerHeight - 24))
    const spaceBelow = window.innerHeight - r.bottom - gap - 12
    const spaceAbove = r.top - gap - 12
    const openUp = spaceBelow < 200 && spaceAbove > spaceBelow
    const avail = Math.max(140, openUp ? spaceAbove : spaceBelow)
    const height = Math.min(maxH, avail)
    setMenuBox({
      left: Math.max(8, Math.min(r.left, window.innerWidth - r.width - 8)),
      width: r.width,
      top: openUp ? undefined : r.bottom + gap,
      bottom: openUp ? window.innerHeight - r.top + gap : undefined,
      maxHeight: height,
    })
  }, [])

  useEffect(() => {
    if (!open) {
      setQuery('')
      setActiveTab(0)
      setMenuBox(null)
      return
    }
    placeMenu()
    const onDoc = (e) => {
      if (ref.current?.contains(e.target) || menuRef.current?.contains(e.target)) return
      setOpen(false)
    }
    const onReposition = () => placeMenu()
    document.addEventListener('mousedown', onDoc)
    window.addEventListener('resize', onReposition)
    // Capture scroll from nested panels so the fixed menu tracks the trigger.
    window.addEventListener('scroll', onReposition, true)
    return () => {
      document.removeEventListener('mousedown', onDoc)
      window.removeEventListener('resize', onReposition)
      window.removeEventListener('scroll', onReposition, true)
    }
  }, [open, placeMenu])

  // Normalize to labelled sections so flat `options` and grouped `groups` share
  // one render path. A null section label renders no header.
  const sections = groups ? groups : [{ label: null, options }]
  const allOptions = sections.flatMap(s => s.options)
  const selectedValues = multiple ? (Array.isArray(value) ? value.map(String) : []) : []
  const isSelected = (o) => multiple ? selectedValues.includes(String(o.value)) : String(o.value) === String(value)
  const selected = !multiple && allOptions.find(o => String(o.value) === String(value))
  const triggerLabel = multiple
    ? (selectedValues.length ? `已选 ${selectedValues.length} 项` : placeholder)
    : (selected ? selected.label : placeholder)
  const hasSelection = multiple ? selectedValues.length > 0 : !!selected
  const q = query.trim().toLowerCase()
  // Tab mode (with groups): show one group at a time behind a tab bar so long
  // node lists don't bury the second group below a scroll. The header per
  // section is dropped (the tab already labels it).
  const useTabs = tabs && sections.length > 1
  const baseSections = useTabs ? [{ label: null, options: (sections[activeTab] || sections[0]).options }] : sections
  const shownSections = baseSections
    .map(s => ({ label: s.label, options: searchable && q ? s.options.filter(o => String(o.label).toLowerCase().includes(q)) : s.options }))
    .filter(s => s.options.length > 0)
  const empty = shownSections.length === 0
  const choose = (o) => {
    const sv = String(o.value)
    if (multiple) {
      onChange(selectedValues.includes(sv) ? selectedValues.filter(x => x !== sv) : [...selectedValues, sv])
    } else {
      onChange(sv); setOpen(false)
    }
  }
  const renderOption = (o) => {
    const sel = isSelected(o)
    return (
      <button key={String(o.value)} type="button"
        onClick={() => choose(o)}
        className={`w-full text-left px-3 py-1.5 text-[13.5px] transition-colors hover:bg-raised flex items-center gap-2 ${sel ? 'text-emerald-600 font-semibold' : 'text-ink'}`}>
        {multiple && (
          <span className={`w-3.5 h-3.5 flex-none rounded border flex items-center justify-center ${sel ? 'bg-emerald-600 border-emerald-600' : 'border-line'}`}>
            {sel && <svg className="w-2.5 h-2.5 text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round"><path d="M20 6L9 17l-5-5"/></svg>}
          </span>
        )}
        {o.icon}
        <span className="truncate">{o.label}</span>
      </button>
    )
  }
  // Header (tabs/search) stays put; only the option list scrolls inside the
  // remaining height so long node lists stay fully selectable.
  const headerH = (useTabs ? 48 : 0) + (searchable ? 58 : 0)
  const listMaxH = menuBox ? Math.max(96, menuBox.maxHeight - headerH) : 220

  // Menu is portaled to document.body so position:fixed is relative to the
  // viewport. Nested Modal/card transforms (e.g. animate-in) would otherwise
  // re-root fixed coordinates and fling the menu off-screen.
  const menu = open && menuBox ? createPortal(
    <div ref={menuRef}
      style={{
        position: 'fixed',
        left: menuBox.left,
        width: menuBox.width,
        top: menuBox.top,
        bottom: menuBox.bottom,
        maxHeight: menuBox.maxHeight,
        zIndex: 200,
      }}
      className="bg-surface border border-line rounded-[11px] shadow-[0_20px_50px_-16px_rgba(0,0,0,0.7)] overflow-hidden flex flex-col">
      {useTabs && (
        <div className="flex border-b border-line-soft flex-none">
          {sections.map((s, i) => (
            <button key={i} type="button" onClick={() => setActiveTab(i)}
              className={`flex-1 px-3 py-[13px] text-[14px] font-semibold transition-colors ${i === activeTab ? 'text-emerald-600 border-b-2 border-emerald-600 -mb-px' : 'text-ink-soft hover:text-ink'}`}>
              {s.label} <span className="text-ink-mut font-normal">{s.options.length}</span>
            </button>
          ))}
        </div>
      )}
      {searchable && (
        <div className="p-3 border-b border-line-soft flex-none">
          <input autoFocus value={query} onChange={e => setQuery(e.target.value)} placeholder="搜索…"
            onKeyDown={e => { if (e.key === 'Enter') e.preventDefault() }}
            className="input-field w-full text-[13px]" style={{ height: 34 }} />
        </div>
      )}
      <div className="overflow-y-auto py-1.5 px-1.5 min-h-0" style={{ maxHeight: listMaxH }}>
        {empty ? (
          <div className="px-3 py-2 text-[13px] text-ink-mut">无匹配</div>
        ) : shownSections.map((s, i) => (
          <div key={i}>
            {s.label && <div className="px-3 pt-1.5 pb-0.5 text-[11px] font-semibold uppercase tracking-wider text-ink-mut">{s.label}</div>}
            {s.options.map(renderOption)}
          </div>
        ))}
      </div>
    </div>,
    document.body,
  ) : null

  return (
    <div ref={ref} className={`relative ${className}`} style={style}>
      <button ref={triggerRef} type="button" disabled={disabled} onClick={() => setOpen(o => !o)}
        className="input-field flex items-center justify-between gap-2 text-left disabled:opacity-60 disabled:cursor-not-allowed">
        <span className={`flex items-center gap-1.5 min-w-0 ${hasSelection ? 'text-ink' : 'text-ink-mut'}`}>
          {selected && selected.icon}
          <span className="truncate">{triggerLabel}</span>
        </span>
        <svg className={`w-4 h-4 flex-none text-ink-mut transition-transform ${open ? 'rotate-180' : ''}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m6 9 6 6 6-6"/></svg>
      </button>
      {menu}
    </div>
  )
}

/* ---------- DateInput ---------- */
// Custom date picker (yyyy-mm-dd). Calendar is portaled to document.body so
// TableBox / detail-panel / overflow-hidden containers cannot clip it the way
// a native <input type="date"> popup is clipped on Chromium.
const WEEKDAYS_ZH = ['日', '一', '二', '三', '四', '五', '六']

function parseYMD(s) {
  if (!s || typeof s !== 'string') return null
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s.trim())
  if (!m) return null
  const y = Number(m[1]), mo = Number(m[2]), d = Number(m[3])
  const dt = new Date(y, mo - 1, d)
  if (dt.getFullYear() !== y || dt.getMonth() !== mo - 1 || dt.getDate() !== d) return null
  return dt
}

function toYMD(dt) {
  if (!dt) return ''
  const y = dt.getFullYear()
  const m = String(dt.getMonth() + 1).padStart(2, '0')
  const d = String(dt.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

function sameDay(a, b) {
  return a && b && a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
}

export function DateInput({
  value = '',
  onChange,
  className = '',
  style,
  disabled = false,
  placeholder = '选择日期',
  allowClear = true,
}) {
  const triggerRef = useRef(null)
  const panelRef = useRef(null)
  const [open, setOpen] = useState(false)
  const [box, setBox] = useState(null) // { left, top, width, placeUp }
  const selected = parseYMD(value)
  const today = useMemo(() => {
    const t = new Date()
    return new Date(t.getFullYear(), t.getMonth(), t.getDate())
  }, [])
  const [view, setView] = useState(() => {
    const base = selected || today
    return { y: base.getFullYear(), m: base.getMonth() }
  })

  const place = useCallback(() => {
    const el = triggerRef.current
    if (!el) return
    const r = el.getBoundingClientRect()
    const panelH = 320
    const panelW = Math.max(280, r.width)
    const spaceBelow = window.innerHeight - r.bottom - 8
    const placeUp = spaceBelow < panelH && r.top > spaceBelow
    let left = r.left
    if (left + panelW > window.innerWidth - 8) left = Math.max(8, window.innerWidth - panelW - 8)
    if (left < 8) left = 8
    const top = placeUp ? Math.max(8, r.top - panelH - 6) : Math.min(window.innerHeight - panelH - 8, r.bottom + 6)
    setBox({ left, top, width: panelW, placeUp })
  }, [])

  useEffect(() => {
    if (!open) return
    const base = selected || today
    setView({ y: base.getFullYear(), m: base.getMonth() })
    place()
    const onScroll = () => place()
    const onResize = () => place()
    const onDown = (e) => {
      if (triggerRef.current?.contains(e.target)) return
      if (panelRef.current?.contains(e.target)) return
      setOpen(false)
    }
    const onKey = (e) => { if (e.key === 'Escape') setOpen(false) }
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', onResize)
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', onResize)
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open, place, selected, today])

  const days = useMemo(() => {
    const first = new Date(view.y, view.m, 1)
    const startPad = first.getDay() // 0 Sun
    const dim = new Date(view.y, view.m + 1, 0).getDate()
    const prevDim = new Date(view.y, view.m, 0).getDate()
    const cells = []
    for (let i = 0; i < startPad; i++) {
      const d = prevDim - startPad + 1 + i
      cells.push({ date: new Date(view.y, view.m - 1, d), outside: true })
    }
    for (let d = 1; d <= dim; d++) {
      cells.push({ date: new Date(view.y, view.m, d), outside: false })
    }
    while (cells.length % 7 !== 0) {
      const d = cells.length - (startPad + dim) + 1
      cells.push({ date: new Date(view.y, view.m + 1, d), outside: true })
    }
    return cells
  }, [view])

  const pick = (dt) => {
    onChange?.(toYMD(dt))
    setOpen(false)
  }

  const shiftMonth = (delta) => {
    setView(v => {
      const d = new Date(v.y, v.m + delta, 1)
      return { y: d.getFullYear(), m: d.getMonth() }
    })
  }

  const label = selected
    ? `${selected.getFullYear()}/${String(selected.getMonth() + 1).padStart(2, '0')}/${String(selected.getDate()).padStart(2, '0')}`
    : ''

  const panel = open && box ? createPortal(
    <div
      ref={panelRef}
      role="dialog"
      aria-label="选择日期"
      className="fixed z-[90] rounded-2xl border border-line bg-surface shadow-[0_20px_50px_-16px_rgba(0,0,0,0.55)] p-3 select-none"
      style={{ left: box.left, top: box.top, width: box.width }}
    >
      <div className="flex items-center justify-between gap-2 mb-2 px-0.5">
        <button type="button" className="w-8 h-8 rounded-lg text-ink-soft hover:bg-raised grid place-items-center" onClick={() => shiftMonth(-1)} aria-label="上个月">‹</button>
        <div className="text-[13.5px] font-bold text-ink tabular-nums">
          {view.y}年{view.m + 1}月
        </div>
        <button type="button" className="w-8 h-8 rounded-lg text-ink-soft hover:bg-raised grid place-items-center" onClick={() => shiftMonth(1)} aria-label="下个月">›</button>
      </div>
      <div className="grid grid-cols-7 gap-0.5 mb-1">
        {WEEKDAYS_ZH.map(w => (
          <div key={w} className="text-center text-[11px] font-semibold text-ink-mut py-1">{w}</div>
        ))}
      </div>
      <div className="grid grid-cols-7 gap-0.5">
        {days.map(({ date, outside }, i) => {
          const isSel = sameDay(date, selected)
          const isToday = sameDay(date, today)
          return (
            <button
              key={i}
              type="button"
              onClick={() => pick(date)}
              className={`h-8 rounded-lg text-[12.5px] font-semibold tabular-nums transition-colors
                ${outside ? 'text-ink-mut/45' : 'text-ink'}
                ${isSel ? 'bg-emerald-600 text-white hover:bg-emerald-600' : 'hover:bg-raised'}
                ${!isSel && isToday ? 'ring-1 ring-emerald-500/50 text-emerald-700 dark:text-emerald-400' : ''}
              `}
            >
              {date.getDate()}
            </button>
          )
        })}
      </div>
      <div className="flex items-center justify-between gap-2 mt-2 pt-2 border-t border-line-soft">
        <button type="button" className="text-[12px] font-semibold text-emerald-600 hover:underline" onClick={() => pick(today)}>今天</button>
        {allowClear && (
          <button type="button" className="text-[12px] font-semibold text-ink-mut hover:text-ink hover:underline" onClick={() => { onChange?.(''); setOpen(false) }}>清除</button>
        )}
      </div>
    </div>,
    document.body,
  ) : null

  return (
    <div className={`relative inline-block ${className}`} style={style}>
      <button
        ref={triggerRef}
        type="button"
        disabled={disabled}
        onClick={() => !disabled && setOpen(o => !o)}
        className="input-field flex items-center justify-between gap-2 text-left font-mono disabled:opacity-60 disabled:cursor-not-allowed"
        style={style}
      >
        <span className={`truncate ${label ? 'text-ink' : 'text-ink-mut'}`}>{label || placeholder}</span>
        <svg className="w-4 h-4 flex-none text-ink-mut" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="4" width="18" height="18" rx="2" />
          <path d="M16 2v4M8 2v4M3 10h18" />
        </svg>
      </button>
      {panel}
    </div>
  )
}
