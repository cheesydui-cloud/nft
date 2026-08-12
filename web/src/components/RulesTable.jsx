import { useState, useRef, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { Badge, ProtoBadge, SensText, CopyText, Tooltip, Spinner, NodeTypeIcon } from './ui'
import { useCopyFmt, useToast } from './Layout'
import { fmtBytes, fmtDate, isExpired, expiryBadge } from '../lib/fmt'
import { buildRelayDisplayName } from '../lib/landing'
import { formatRelayCopyText, formatRuleCopyText, relayExpiryFromMap } from '../lib/relayCopy'
import { createLimiter } from '../lib/limiter'
import { useIsMobile } from '../lib/useIsMobile'
import { useRuleSpeed, fmtSpeed } from '../lib/useSpeed'
import { HealthDot } from './HealthDot'
import { QRCodeButton } from './QRCodeModal'

// Cap concurrent connectivity probes so "test all" doesn't fire one request per
// rule at once (each fans out to every hop on the server side).
const probeLimit = createLimiter(6)

/* Shared rule table for both the admin (`/rules`) and user (`/my/rules`) lists.
   variant drives the columns that differ: admin shows id/owner and links to a
   detail page; my shows traffic. Everything else — name, node, proto, entry,
   exit, sorting, alignment — is identical so the two pages stay in lockstep. */

const exitOf = (r) => (r.exit_host && r.exit_port ? `${r.exit_host}:${r.exit_port}` : '')

/* Geometric triangles render as plain text glyphs; the arrow characters
   (↑↓↕) get emoji-styled on some platforms, which breaks column alignment. */
function SortArrow({ dir }) {
  return (
    <span className="inline-flex flex-col leading-[0.55] text-[9px] ml-1">
      <span className={dir === 'asc' ? 'text-emerald-600' : 'text-ink-mut opacity-50'}>▲</span>
      <span className={dir === 'desc' ? 'text-emerald-600' : 'text-ink-mut opacity-50'}>▼</span>
    </span>
  )
}


function lineLabel(r, node) {
  const nodeName = node?.name || `#${r.node_id}`
  const svc = (r.landing_name || '').trim()
  const proto = (r.landing_protocol || '').toLowerCase()
  // Protocol-entry / 代理 tab: show service name with node, e.g. [vless] 测试1 · NB.JP
  if (svc && Number(r.proxy_service_id) > 0) {
    const tag = proto && proto !== 'socks5' && proto !== 'sk5' ? `[${proto}] ` : ''
    return `${tag}${svc} · ${nodeName}`
  }
  return nodeName
}

export function RulesTable({ rules, nodeMap, blurred, variant = 'my', onDelete, onEdit, onCopy, onRowClick, probeAllTrigger, displayRate = 1, landingExpiry, copyUsername = '' }) {
  const isAdmin = variant === 'admin'
  const isMobile = useIsMobile()
  const [sort, setSort] = useState({ col: null, dir: null })
  const { copyFmt } = useCopyFmt()
  const toast = useToast()
  // Live rates are admin-only: the user list hides the speed column entirely.
  // Per-rule only (never node totals) so rules sharing a relay show independent ↑/↓.
  const ruleSpeeds = useRuleSpeed({ enabled: isAdmin })

  const ownerForCopy = (r) => r.owner_name || copyUsername || ''

  // QR always uses raw URI (not YAML) for client scanners.
  const ruleQRText = (r) => formatRuleCopyText(r, {
    username: ownerForCopy(r),
    expiryMap: landingExpiry,
    asYaml: false,
  })

  const renderLandingExpiry = (r) => {
    // Prefer server field (user_landing_exits); map is fallback for older payloads.
    const ts = (r.landing_expires_at > 0)
      ? r.landing_expires_at
      : (landingExpiry ? landingExpiry.get(exitOf(r)) : 0)
    if (!ts || ts <= 0) return null
    const badge = expiryBadge(ts)
    if (!badge) return null
    return <Badge color={badge.color} className="ml-1 whitespace-nowrap">{badge.label}</Badge>
  }

  const cycleSort = (col) => {
    setSort(s => {
      if (s.col !== col) return { col, dir: 'asc' }
      if (s.dir === 'asc') return { col, dir: 'desc' }
      return { col: null, dir: null }
    })
  }

  const sorted = !sort.col ? rules : [...rules].sort((a, b) => {
    if (sort.col === 'traffic') {
      const d = (a.exit_bytes || 0) - (b.exit_bytes || 0)
      return sort.dir === 'asc' ? d : -d
    }
    const va = sort.col === 'node' ? (nodeMap[a.node_id]?.name || '') : (a.owner_name || '')
    const vb = sort.col === 'node' ? (nodeMap[b.node_id]?.name || '') : (b.owner_name || '')
    const c = va.localeCompare(vb)
    return sort.dir === 'asc' ? c : -c
  })

  if (isMobile) return renderCards()

  return (
    <table className="tbl">
      <thead>
        <tr>
          <th>{isAdmin ? '规则' : '名称'}</th>
          <th className="cursor-pointer select-none" onClick={() => cycleSort('node')}>
            <span className="inline-flex items-center">节点<SortArrow dir={sort.col === 'node' ? sort.dir : null} /></span>
          </th>
          {isAdmin && <th>链路</th>}
          {!isAdmin && <th>协议</th>}
          {isAdmin && <th className="whitespace-nowrap">实时</th>}
          <th className="text-right cursor-pointer select-none" onClick={() => cycleSort('traffic')}>
            <span className="inline-flex items-center justify-end">流量<SortArrow dir={sort.col === 'traffic' ? sort.dir : null} /></span>
          </th>
          <th className="text-right w-[1%] whitespace-nowrap">操作</th>
        </tr>
      </thead>
      <tbody>
        {sorted.map(r => {
          const node = nodeMap[r.node_id]
          const sp = isAdmin ? (ruleSpeeds[r.id] || { up: 0, down: 0 }) : null
          return (
            <tr key={r.id}
              className={onRowClick ? 'cursor-pointer' : ''}
              onClick={onRowClick ? () => onRowClick(r) : undefined}>
              {/* 规则：主行名称 · 副行 #ID · 所有者 · 备注 */}
              <td className="!whitespace-normal min-w-[8rem]">
                <div className="font-semibold text-[13.5px] text-ink leading-snug">{r.name}</div>
                <div className="mt-0.5 flex items-center gap-1.5 flex-wrap text-[11.5px] text-ink-mut">
                  {isAdmin && <span className="font-mono">#{r.id}</span>}
                  {isAdmin && r.owner_name && (
                    <>
                      <span className="opacity-40">·</span>
                      <span className="text-ink-soft">{r.owner_name}</span>
                    </>
                  )}
                  {r.comment && (
                    <>
                      <span className="opacity-40">·</span>
                      {r.comment.length > 16
                        ? <Tooltip content={r.comment}><span className="cursor-help truncate max-w-[10rem]">{r.comment.slice(0, 16)}…</span></Tooltip>
                        : <span className="truncate max-w-[10rem]">{r.comment}</span>}
                    </>
                  )}
                </div>
              </td>

              <td>
                <span className="inline-flex items-center gap-1.5 font-mono text-[13px] text-ink-soft">
                  <HealthDot online={node?.online} disabled={!!node?.disabled} showLabel={false} />
                  <NodeTypeIcon type={node?.node_type} />
                  <span className={Number(r.proxy_service_id) > 0 ? 'font-sans' : ''}>{lineLabel(r, node)}</span>
                  {r.via_node_ids?.length > 0 && <span className="text-ink-mut text-[11px] font-sans">+{r.via_node_ids.length}层</span>}
                </span>
              </td>

              {isAdmin && (
              <td className="font-mono text-xs !whitespace-normal min-w-[12rem]">
                <div className="inline-block max-w-[22rem]" onClick={e => e.stopPropagation()}>
                  {(() => {
                    const nodeLabel = lineLabel(r, node)
                    const shown = r.entry_listen_port ? `${nodeLabel}:${r.entry_listen_port}` : nodeLabel
                    return (
                      <>
                        <div className="flex items-center gap-1.5 leading-snug">
                          <span className="text-[10.5px] font-semibold text-ink-mut shrink-0 w-7">{r.entry_v6 ? '入v4' : '入口'}</span>
                          {r.entry
                            ? <CopyText text={r.entry}><span className="font-sans text-[12.5px] text-ink">{shown}</span></CopyText>
                            : <span className="text-ink-mut">—</span>}
                        </div>
                        {r.entry_v6 && (
                          <div className="flex items-center gap-1.5 leading-snug mt-0.5">
                            <span className="text-[10.5px] font-semibold text-ink-mut shrink-0 w-7">入v6</span>
                            <CopyText text={r.entry_v6}><span className="font-sans text-[12.5px] text-ink">{shown}</span></CopyText>
                          </div>
                        )}
                      </>
                    )
                  })()}
                  {(() => {
                    const exitLabel = !isAdmin && r.exit_kind === 'landing' && r.landing_name
                      ? <span className="font-sans text-[12.5px]">{r.landing_name}{renderLandingExpiry(r)}</span>
                      : <SensText blurred={blurred}><span className="text-[12.5px]">{exitOf(r) || '—'}</span></SensText>
                    const protoTag = (r.landing_protocol || (r.exit_type === 'socks5' ? 'sk5' : '') || '').toLowerCase()
                    const proxyRow = (uri, tag) => {
                      const expiresAt = (r.landing_expires_at > 0)
                        ? r.landing_expires_at
                        : relayExpiryFromMap(landingExpiry, r.exit_host, r.exit_port)
                      const baseName = buildRelayDisplayName({
                        username: ownerForCopy(r),
                        ruleName: r.name || '',
                        expiresAt,
                      })
                      const displayName = tag === 'v6' && baseName ? `${baseName}-v6` : (baseName || undefined)
                      const text = formatRelayCopyText(uri, {
                        username: ownerForCopy(r),
                        ruleName: r.name || '',
                        expiresAt,
                        asYaml: copyFmt === 'yaml',
                        displayName,
                      })
                      return (
                        <div className="flex items-center gap-1.5 flex-wrap text-ink-soft mt-0.5 leading-snug">
                          <span className="text-[10.5px] font-semibold text-ink-mut shrink-0 w-7">{tag || '出口'}</span>
                          {protoTag && <span className="font-mono text-[10.5px] font-bold uppercase text-[var(--brand-to)] opacity-80">{protoTag}</span>}
                          <CopyText text={text || uri}>{exitLabel}</CopyText>
                        </div>
                      )
                    }
                    if (r.relay_uri && r.relay_uri_v6) {
                      return <>{proxyRow(r.relay_uri, '出v4')}{proxyRow(r.relay_uri_v6, '出v6')}</>
                    }
                    if (r.relay_uri) return proxyRow(r.relay_uri, '出口')
                    return (
                      <div className="flex items-center gap-1.5 flex-wrap text-ink-soft mt-0.5 leading-snug">
                        <span className="text-[10.5px] font-semibold text-ink-mut shrink-0 w-7">出口</span>
                        {r.exit_type === 'socks5'
                          ? <span className="font-mono text-[10.5px] font-bold uppercase text-[var(--brand-to)] opacity-80">sk5</span>
                          : (protoTag ? <span className="font-mono text-[10.5px] font-bold uppercase text-[var(--brand-to)] opacity-80">{protoTag}</span> : null)}
                        {exitLabel}
                        {r.exit_type === 'socks5' && r.exit_uri && (
                          <span className="text-[11px] text-ink-mut font-mono truncate max-w-[160px]" title={r.exit_uri}>
                            via {r.exit_uri}
                          </span>
                        )}
                      </div>
                    )
                  })()}
                </div>
              </td>
              )}

              {!isAdmin && <td><ProtoBadge proto={r.proto} /></td>}

              {isAdmin && (
              <td className="font-mono text-xs whitespace-nowrap">
                <span className={`inline-flex items-center gap-1.5 ${(sp.up || sp.down) ? 'text-[var(--brand-to)]' : 'text-ink-mut'}`}>
                  <span>↑{fmtSpeed(sp.up)}</span>
                  <span>↓{fmtSpeed(sp.down)}</span>
                </span>
              </td>
              )}

              <td className="text-right font-mono text-xs text-ink-mut">{fmtBytes(Math.round(((r.exit_bytes || 0)) * displayRate))}</td>
              <td className="text-right whitespace-nowrap">
                <div className="inline-flex gap-1.5 justify-end items-center" onClick={e => e.stopPropagation()}>
                  {isAdmin && onCopy && (
                    <button type="button" onClick={() => onCopy(r)} className="row-action-btn" title="复制规则链接">复制</button>
                  )}
                  {isAdmin && <QRCodeButton text={ruleQRText(r)} toast={toast} className="row-action-btn" />}
                  <ProbeIconButton ruleId={r.id} probeAllTrigger={probeAllTrigger} />
                  <MoreMenu items={[
                    onEdit && { label: '编辑', onClick: () => onEdit(r) },
                    !isAdmin && onCopy && { label: '复制', onClick: () => onCopy(r) },
                    { label: '删除', onClick: () => onDelete(r), danger: true },
                  ].filter(Boolean)} />
                </div>
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )

  function renderCards() {
    return (
    <div>
      {sorted.map(r => {
        const node = nodeMap[r.node_id]
        const sp = isAdmin ? (ruleSpeeds[r.id] || { up: 0, down: 0 }) : null
        return (
          <div key={r.id} className={`mobile-card ${onRowClick ? 'cursor-pointer' : ''}`}
            onClick={onRowClick ? () => onRowClick(r) : undefined}>
            <div className="flex items-center justify-between mb-1">
              <span className="font-semibold text-[14px]">{r.name}</span>
              <div className="flex items-center gap-2">
                <ProbeIconButton ruleId={r.id} probeAllTrigger={probeAllTrigger} />
                {isAdmin && <QRCodeButton text={ruleQRText(r)} toast={toast} className="row-action-btn" />}
                <ProtoBadge proto={r.proto} />
              </div>
            </div>
            <div className="flex items-center gap-2 text-xs text-ink-soft mb-1.5 flex-wrap">
              <span className="inline-flex items-center gap-1 font-mono">
                <HealthDot online={node?.online} disabled={!!node?.disabled} showLabel={false} />
                <NodeTypeIcon type={node?.node_type} />
                <span className={Number(r.proxy_service_id) > 0 ? 'font-sans' : ''}>{lineLabel(r, node)}</span>
                {!isAdmin && r.via_node_ids?.length > 0 && <span className="text-ink-mut text-[11px] font-sans">+{r.via_node_ids.length}层</span>}
              </span>
              {isAdmin && r.owner_name && <><span className="text-ink-mut">·</span><span>{r.owner_name}</span></>}
              {isAdmin && (
                <>
                  <span className="text-ink-mut">·</span>
                  <span className="font-mono text-emerald-600">↑{fmtSpeed(sp.up)} ↓{fmtSpeed(sp.down)}</span>
                </>
              )}
              <span className="text-ink-mut">·</span>
              <span className="font-mono text-ink-mut">{fmtBytes(Math.round((r.exit_bytes || 0) * displayRate))}</span>
            </div>
            {isAdmin && (
            <div className="text-xs text-ink-mut truncate">
              <span className="font-sans">{r.entry ? (r.entry_listen_port ? `${lineLabel(r, node)}:${r.entry_listen_port}` : lineLabel(r, node)) : '--'}</span>
              <span className="mx-1.5">→</span>
              <span className="text-ink-soft font-mono">
                {!isAdmin && r.exit_kind === 'landing' && r.landing_name
                  ? <span className="font-sans">{r.landing_name}{renderLandingExpiry(r)}</span>
                  : <SensText blurred={blurred}>{exitOf(r) || '--'}</SensText>}
              </span>
            </div>
            )}
          </div>
        )
      })}
    </div>
    )
  }
}

function ProbeIconButton({ ruleId, probeAllTrigger }) {
  const [state, setState] = useState('idle')
  const [label, setLabel] = useState('')
  const [tip, setTip] = useState('')
  const [latencyMs, setLatencyMs] = useState(null)
  useEffect(() => {
    if (probeAllTrigger) probe()
  }, [probeAllTrigger])
  const probe = () => {
    setState('loading')
    probeLimit(() => fetch(`/api/probe-chain?rule_id=${ruleId}`).then(r => r.json())).then(d => {
      if (d.hops?.length) {
        const parts = d.hops.map(h => h.error ? 'x' : h.latency_ms + 'ms')
        const joined = parts.join(' → ')
        if (d.ok) {
          setState('ok')
          setLatencyMs(d.latency_ms || 0)
          setLabel(d.hops.length > 1 ? joined + ' = ' + d.latency_ms + 'ms' : d.latency_ms + 'ms')
          setTip(joined + ' = ' + d.latency_ms + 'ms')
        } else {
          setState('fail')
          setLatencyMs(null)
          setLabel(joined)
          setTip(joined)
        }
      } else if (d.ok) {
        setState('ok')
        setLatencyMs(d.latency_ms || 0)
        setLabel(d.latency_ms + 'ms')
        setTip('')
      } else {
        setState('fail')
        setLatencyMs(null)
        setLabel(d.error || '不通')
        setTip('')
      }
    }).catch(() => { setState('fail'); setLatencyMs(null); setLabel('失败'); setTip('') })
  }
  const probeOk = state === 'ok' ? true : state === 'fail' ? false : null
  return (
    <span className="inline-flex items-center gap-1">
      <button onClick={probe} disabled={state === 'loading'} title={tip || label || '测试连通性'}
        className={`icon-btn ${state === 'ok' ? '!text-green-500 !border-green-500/30' : state === 'fail' ? '!text-red-400 !border-red-500/30' : ''}`}>
        {state === 'loading' ? <Spinner className="w-4 h-4" /> : <IconPulse />}
      </button>
      {state !== 'idle' && state !== 'loading' && (
        <HealthDot probeOk={probeOk} latencyMs={latencyMs} showLabel />
      )}
    </span>
  )
}

export function MoreMenu({ items }) {
  const [open, setOpen] = useState(false)
  const [dropUp, setDropUp] = useState(false)
  const ref = useRef(null)
  const menuRef = useRef(null)
  useEffect(() => {
    if (!open) return
    const h = (e) => { if (ref.current && !ref.current.contains(e.target)) setOpen(false) }
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open])
  useEffect(() => {
    if (!open || !menuRef.current) return
    const rect = menuRef.current.getBoundingClientRect()
    let maxBottom = window.innerHeight - 8
    for (let el = menuRef.current.parentElement; el; el = el.parentElement) {
      const s = getComputedStyle(el)
      if (s.overflow === 'hidden' || s.overflow === 'auto' || s.overflowY === 'hidden' || s.overflowY === 'auto') {
        maxBottom = Math.min(maxBottom, el.getBoundingClientRect().bottom - 8)
        break
      }
    }
    if (rect.bottom > maxBottom) setDropUp(true)
  }, [open])
  const toggle = () => { setDropUp(false); setOpen(o => !o) }
  const pos = dropUp ? 'bottom-[calc(100%+4px)]' : 'top-[calc(100%+4px)]'
  return (
    <div ref={ref} className="relative">
      <button onClick={toggle} className="icon-btn">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><circle cx="12" cy="5" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="12" cy="19" r="1"/></svg>
      </button>
      {open && (
        <div ref={menuRef} className={`absolute right-0 ${pos} z-50 min-w-[100px] bg-surface border border-line rounded-lg shadow-[0_8px_30px_-8px_rgba(0,0,0,0.5)] py-1`}>
          {items.map((item, i) => item.href ? (
            <Link key={i} to={item.href} className="block px-3.5 py-2 text-[13px] text-ink hover:bg-raised transition-colors no-underline">{item.label}</Link>
          ) : (
            <button key={i} onClick={() => { setOpen(false); item.onClick() }}
              className={`block w-full text-left px-3.5 py-2 text-[13px] transition-colors bg-transparent border-0 cursor-pointer ${item.danger ? 'text-red-600 hover:bg-red-50' : 'text-ink hover:bg-raised'}`}>{item.label}</button>
          ))}
        </div>
      )}
    </div>
  )
}

const I = (d) => <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">{d}</svg>
function IconPulse() { return I(<path d="M22 12h-4l-3 9L9 3l-3 9H2" />) }
