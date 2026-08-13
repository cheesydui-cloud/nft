import { useState, useEffect, useRef } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '../../lib/api'
import { fmtTime, fmtBytes, nullStr } from '../../lib/fmt'
import { useSpeed, fmtSpeed } from '../../lib/useSpeed'
import { useIsMobile } from '../../lib/useIsMobile'
import { partitionRouteNodes, isLandingListGroup } from '../../lib/routeNodes'
import { Layout, useToast } from '../../components/Layout'
import { Loading, Empty, Badge, Modal, Confirm, NodeStackBadge, NodeBillingBadges, useConfirm, Select, CopyText } from '../../components/ui'
import { PageHeader, Panel, PanelToolbar, SearchInput, ToolbarButton, ToolbarActions, TableScroll } from '../../components/page'

export default function NodeList() {
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showAdd, setShowAdd] = useState(false)
  const [showComposite, setShowComposite] = useState(false)
  // 搜索词存 sessionStorage：进详情返回后过滤还在，但不像 tab 那样跨会话记住。
  const [search, setSearch] = useState(() => sessionStorage.getItem('nodes.search') || '')
  const [tab, setTab] = useState(() => {
    const t = localStorage.getItem('nodes.tab') || 'single'
    return t === 'landing' || t === 'composite' ? t : 'single'
  })
  const [selected, setSelected] = useState(new Set())
  const [dragIndex, setDragIndex] = useState(null)
  const speeds = useSpeed()
  const isMobile = useIsMobile()
  const toast = useToast()
  const confirm = useConfirm()
  const navigate = useNavigate()
  const [sort, setSort] = useState({ col: null, dir: null })
  const [speedSnap, setSpeedSnap] = useState(null)
  const [pinMode, setPinMode] = useState(null)
  // 原始流量默认隐藏，减列宽噪音
  const [showRawTraffic, setShowRawTraffic] = useState(() => localStorage.getItem('nodes.showRaw') === '1')
  const pinRef = useRef(null)
  const pinClickGuard = useRef(false)
  const listRef = useRef(null)

  useEffect(() => { localStorage.setItem('nodes.tab', tab) }, [tab])
  useEffect(() => { sessionStorage.setItem('nodes.search', search) }, [search])
  useEffect(() => { localStorage.setItem('nodes.showRaw', showRawTraffic ? '1' : '0') }, [showRawTraffic])
  useEffect(() => { setSelected(new Set()) }, [tab])

  const load = (silent = false) => {
    if (!silent) { setLoading(true); setError('') }
    api.get('/nodes').then(setData).catch(err => {
      if (!silent) setError(err?.message || '加载失败')
    }).finally(() => { if (!silent) setLoading(false) })
  }
  useEffect(() => { load() }, [])

  const resyncAll = async () => {
    if (!(await confirm({ title: '同步所有节点', message: '向所有节点重新推送转发规则？', confirmText: '同步' }))) return
    try { await api.post('/nodes/resync-all'); toast('已发起同步'); load(true) } catch (err) { toast(err.message, 'error') }
  }

  const deleteNode = async (node) => {
    if (!(await confirm({ title: '删除节点', message: `确认删除节点 ${node.name}？此操作会清空节点上的转发。`, confirmText: '删除', danger: true }))) return
    try { await api.del(`/nodes/${node.id}`); toast('已删除'); load() } catch (err) { toast(err.message, 'error') }
  }

  const resyncNode = async (id) => {
    try { await api.post(`/nodes/${id}/resync`); toast('已发起同步') } catch (err) { toast(err.message, 'error') }
  }

  const upgradeNode = async (id) => {
    try {
      const res = await api.post(`/nodes/${id}/upgrade`)
      toast(res?.version ? `已排队升级 → ${res.version}` : '已排队升级')
      setTimeout(() => load(true), 4000)
    } catch (err) { toast(err.message, 'error') }
  }

  if (loading && !data) return <Layout><Loading /></Layout>
  if (!data && error) return <Layout><Empty title="加载失败" desc={error}><button onClick={load} className="btn-secondary text-xs mt-3">重试</button></Empty></Layout>

  const { nodes = [], latest_agent_version, latest_agent_sha, node_traffic = {}, node_raw_traffic = {} } = data || {}
  // Older installers (and failed release-tag resolution) write "latest" into
  // /etc/nft/agent.version. When the binary SHA matches the server's target
  // agent SHA, the node is actually on the latest release — show the concrete
  // version label instead of the vague "latest" alias.
  const displayAgentVersion = (n) => {
    if (n.agent_version && n.agent_version !== 'latest') return n.agent_version
    if (n.agent_sha && latest_agent_sha && n.agent_sha === latest_agent_sha) return latest_agent_version
    return n.agent_version || '--'
  }
  const isAgentOutdated = (n) => {
    if (n.agent_sha && latest_agent_sha && n.agent_sha === latest_agent_sha) return false
    if (n.agent_version === 'latest') return false
    return n.agent_version && n.agent_version !== latest_agent_version
  }
  const { single: singleNodes, composite: compositeNodes, landing: landingNodes } = partitionRouteNodes(nodes)
  const tabNodes = tab === 'composite' ? compositeNodes : tab === 'landing' ? landingNodes : singleNodes
  const q = search.trim().toLowerCase()
  const nodeMatchesSearch = (n) => {
    if (!q) return true
    if (String(n.id) === q || `#${n.id}` === q) return true
    if ((n.name || '').toLowerCase().includes(q)) return true
    if ((n.address || '').toLowerCase().includes(q)) return true
    if ((n.relay_host || '').toLowerCase().includes(q)) return true
    if ((n.relay_host_v6 || '').toLowerCase().includes(q)) return true
    return false
  }
  const filtered0 = tabNodes.filter(nodeMatchesSearch)

  const outdatedNodes = nodes.filter(n =>
    n.node_type !== 'composite' && !n.disabled && isAgentOutdated(n)
  )
  const outdatedOnline = outdatedNodes.filter(n => n.online === 1)

  const upgradeOutdated = async () => {
    if (!outdatedOnline.length) {
      toast(outdatedNodes.length ? '落后节点均不在线，无法升级' : '没有需要升级的节点', 'error')
      return
    }
    if (!(await confirm({
      title: '升级落后节点',
      message: `向 ${outdatedOnline.length} 台在线且版本落后的节点推送 agent${latest_agent_version ? ` → ${latest_agent_version}` : ''}（后台执行）？`,
      confirmText: '升级',
    }))) return
    try {
      let ok = 0
      const errs = []
      for (const n of outdatedOnline) {
        try {
          await api.post(`/nodes/${n.id}/upgrade`)
          ok++
        } catch (err) {
          errs.push(`${n.name}: ${err.message}`)
        }
      }
      toast(errs.length ? `已排队 ${ok} 台，失败 ${errs.length}` : `已排队升级 ${ok} 台${latest_agent_version ? ` → ${latest_agent_version}` : ''}`, errs.length ? 'error' : undefined)
      setTimeout(() => load(true), 4000)
      load(true)
    } catch (err) { toast(err.message, 'error') }
  }

  const batchResync = async () => {
    const ids = [...selected].filter(id => {
      const n = nodes.find(x => x.id === id)
      return n && n.node_type !== 'composite'
    })
    if (!ids.length) { toast('所选中无实体节点可同步', 'error'); return }
    if (!(await confirm({ title: '同步选中节点', message: `向 ${ids.length} 个节点重新推送规则？`, confirmText: '同步' }))) return
    try {
      for (const id of ids) await api.post(`/nodes/${id}/resync`)
      toast(`已发起同步 ${ids.length} 个`)
      load(true)
    } catch (err) { toast(err.message, 'error') }
  }

  const batchUpgrade = async () => {
    const targets = [...selected]
      .map(id => nodes.find(x => x.id === id))
      .filter(n => n && n.node_type !== 'composite' && !n.disabled && n.online === 1 && isAgentOutdated(n))
    if (!targets.length) {
      toast('选中项中没有「在线且版本落后」的节点', 'error')
      return
    }
    if (!(await confirm({
      title: '升级选中落后节点',
      message: `将升级 ${targets.length} 台：${targets.map(t => t.name).join('、')}`,
      confirmText: '升级',
    }))) return
    try {
      for (const n of targets) await api.post(`/nodes/${n.id}/upgrade`)
      toast(`已排队升级 ${targets.length} 台`)
      setTimeout(() => load(true), 4000)
      load(true)
    } catch (err) { toast(err.message, 'error') }
  }

  // 「原始流量」列默认隐藏；排序带着 raw 切 tab 时清掉。
  const switchTab = (key) => {
    setTab(key)
    if (key !== 'single') setSort(s => s.col === 'rawtraffic' ? { col: null, dir: null } : s)
  }

  const cycleSort = (col) => {
    setSort(s => {
      if (col === 'speed') setSpeedSnap({ ...speeds })
      if (s.col !== col) return { col, dir: 'desc' }
      if (s.dir === 'desc') return { col, dir: 'asc' }
      return { col: null, dir: null }
    })
  }
  const filtered = !sort.col ? filtered0 : [...filtered0].sort((a, b) => {
    let d = 0
    if (sort.col === 'traffic') {
      d = (node_traffic[a.id] || 0) - (node_traffic[b.id] || 0)
    } else if (sort.col === 'rawtraffic') {
      d = (node_raw_traffic[a.id] || 0) - (node_raw_traffic[b.id] || 0)
    } else if (sort.col === 'speed') {
      const sa = speedSnap || speeds
      const va = sa[a.id] ? (sa[a.id].up + sa[a.id].down) : 0
      const vb = sa[b.id] ? (sa[b.id].up + sa[b.id].down) : 0
      d = va - vb
    }
    return sort.dir === 'asc' ? d : -d
  })
  const toggleOne = (id) => setSelected(s => {
    const next = new Set(s)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })
  const toggleAll = () => {
    const allIds = filtered.map(n => n.id)
    const allSelected = allIds.length > 0 && allIds.every(id => selected.has(id))
    if (allSelected) setSelected(s => { const next = new Set(s); allIds.forEach(id => next.delete(id)); return next })
    else setSelected(s => { const next = new Set(s); allIds.forEach(id => next.add(id)); return next })
  }
  const batchListGroup = async (listGroup) => {
    const ids = [...selected]
    if (!ids.length) return
    try {
      await api.post('/nodes/batch-list-group', { ids, list_group: listGroup || '' })
      toast(listGroup === 'landing' ? `已移入「落地」(${ids.length})` : `已移回「单点/组合」(${ids.length})`)
      setSelected(new Set())
      load()
    } catch (err) { toast(err.message, 'error') }
  }
  // 任何过滤/排序生效时都不能拖拽调序：saveOrder 以可见列表为全量重建顺序，
  // 子集视图下会把被过滤掉的节点从顺序里丢掉。
  const draggable = !sort.col && !q
  const saveOrder = async (visibleList) => {
    const tabIds = visibleList.map(n => n.id)
    const tabSet = new Set(tabIds)
    const rest = nodes.filter(n => !tabSet.has(n.id)).map(n => n.id)
    let allIds
    if (tab === 'single') {
      allIds = [...tabIds, ...rest]
    } else if (tab === 'composite') {
      allIds = [...singleNodes.map(n => n.id), ...tabIds, ...landingNodes.map(n => n.id)]
    } else {
      allIds = [...rest, ...tabIds]
    }
    const byId = Object.fromEntries(nodes.map(n => [n.id, n]))
    setData(d => ({ ...d, nodes: allIds.map(id => byId[id]).filter(Boolean) }))
    try { await api.post('/nodes/reorder', { ids: allIds }); toast('顺序已保存') } catch (err) { toast(err.message, 'error'); load() }
  }
  const onDrop = async (toIndex) => {
    if (dragIndex === null || dragIndex === toIndex) { setDragIndex(null); return }
    const list = [...filtered]
    const [moved] = list.splice(dragIndex, 1)
    list.splice(toIndex, 0, moved)
    setDragIndex(null)
    saveOrder(list)
  }
  const moveToEdge = (idx, edge) => {
    const list = [...filtered]
    const [moved] = list.splice(idx, 1)
    if (edge === 'top') list.unshift(moved); else list.push(moved)
    saveOrder(list)
  }
  const onPinDown = (e, idx) => {
    if (e.button !== 0 || e.target.closest('[draggable], button, a')) return
    pinRef.current = { idx, x0: e.clientX, y0: e.clientY, entered: false }
    e.currentTarget.setPointerCapture(e.pointerId)
  }
  const onPinMove = (e) => {
    const p = pinRef.current
    if (!p) return
    const dx = e.clientX - p.x0, dy = e.clientY - p.y0
    if (!p.entered) {
      if (Math.abs(dx) < 10 && Math.abs(dy) < 10) return
      if (dx < -40 && Math.abs(dx) > Math.abs(dy) * 1.5) { p.entered = true } else if (Math.abs(dy) > 20 || dx > 10) { pinRef.current = null; return }
      if (!p.entered) return
    }
    e.preventDefault()
    const zone = (e.clientY - p.y0) < -20 ? 'top' : (e.clientY - p.y0) > 20 ? 'bottom' : null
    setPinMode({ idx: p.idx, zone, cx: e.clientX, cy: e.clientY })
  }
  const onPinUp = () => {
    const p = pinRef.current
    pinRef.current = null
    if (!p?.entered || !pinMode) { setPinMode(null); return }
    pinClickGuard.current = true
    setTimeout(() => { pinClickGuard.current = false }, 100)
    const { idx, zone } = pinMode
    setPinMode(null)
    if (zone) moveToEdge(idx, zone)
  }

  const showRawCol = showRawTraffic && tab === 'single'

  return (
    <Layout>
      <div className="h-full flex flex-col">
      <PageHeader
        title="节点管理"
        count={nodes.length}
        unit="个"
        badge={latest_agent_version ? (
          <span className="inline-flex items-center gap-2 text-[12.5px] text-ink-mut flex-wrap">
            <span>agent 最新 <span className="font-mono font-semibold text-ink-soft">{latest_agent_version}</span></span>
            {outdatedNodes.length > 0 && (
              <Badge color="amber">落后 {outdatedNodes.length} 台{outdatedOnline.length < outdatedNodes.length ? ` · 在线 ${outdatedOnline.length}` : ''}</Badge>
            )}
          </span>
        ) : null}
      />

      <Panel fill>
        <PanelToolbar>
          <SearchInput value={search} onChange={setSearch} placeholder="搜索名称 / IP / 线路地址 / ID…" />
          <ToolbarActions className="hidden md:flex">
            <ToolbarButton onClick={resyncAll} secondary>同步所有</ToolbarButton>
            <ToolbarButton
              onClick={upgradeOutdated}
              secondary
              title={outdatedOnline.length ? `升级 ${outdatedOnline.length} 台在线落后节点` : '无在线落后节点'}
            >
              升级落后{outdatedOnline.length ? ` (${outdatedOnline.length})` : ''}
            </ToolbarButton>
            <ToolbarButton onClick={() => setShowComposite(true)} secondary>＋ 组合节点</ToolbarButton>
            <ToolbarButton onClick={() => setShowAdd(true)}>＋ 添加节点</ToolbarButton>
          </ToolbarActions>
        </PanelToolbar>
        <div className="flex items-center flex-wrap gap-1.5 px-[22px] py-2.5 border-b border-line-soft">
          {[['single', '单点', singleNodes.length], ['composite', '组合', compositeNodes.length], ['landing', '落地', landingNodes.length]].map(([key, label, n]) => (
            <button key={key} onClick={() => switchTab(key)}
              title={key === 'landing' ? '手动归入「落地」分组的节点（与落地仓库无关）' : undefined}
              className={`chip-btn ${tab === key ? 'is-active' : ''}`}>{label} {n}</button>
          ))}
          {tab === 'single' && (
            <button type="button" onClick={() => setShowRawTraffic(v => !v)}
              className={`text-[11.5px] font-semibold px-2.5 py-1 rounded-full border transition-colors ${
                showRawTraffic
                  ? 'border-emerald-300 text-emerald-700 bg-emerald-50'
                  : 'border-line text-ink-mut hover:bg-raised'
              }`}>
              {showRawTraffic ? '隐藏原始流量' : '显示原始流量'}
            </button>
          )}
          {!draggable && (
            <span className="text-[11px] text-ink-mut">排序/搜索中不可拖拽调序</span>
          )}
          {selected.size > 0 && (
            <div className="ml-auto flex items-center gap-1.5 flex-wrap">
              <span className="text-[12px] text-ink-mut">已选 {selected.size}</span>
              <button type="button" onClick={batchResync}
                className="text-ink-soft text-xs font-semibold px-2.5 py-1 rounded border border-line hover:bg-raised">
                同步
              </button>
              <button type="button" onClick={batchUpgrade}
                className="text-ink-soft text-xs font-semibold px-2.5 py-1 rounded border border-line hover:bg-raised">
                升级落后
              </button>
              {tab === 'landing' ? (
                <button type="button" onClick={() => batchListGroup('')}
                  className="text-emerald-600 text-xs font-semibold px-3 py-1 rounded border border-emerald-200 hover:bg-emerald-50 dark:border-emerald-700 dark:hover:bg-emerald-900/20">
                  移回单点/组合
                </button>
              ) : (
                <button type="button" onClick={() => batchListGroup('landing')}
                  className="text-emerald-600 text-xs font-semibold px-3 py-1 rounded border border-emerald-200 hover:bg-emerald-50 dark:border-emerald-700 dark:hover:bg-emerald-900/20">
                  移入落地
                </button>
              )}
            </div>
          )}
        </div>
        <TableScroll>
        {nodes.length === 0 ? (
          <Empty title="尚未注册任何节点" desc="点击右上角「添加节点」创建。" />
        ) : tabNodes.length === 0 ? (
          <Empty
            title={tab === 'composite' ? '暂无组合节点' : tab === 'landing' ? '暂无落地节点' : '暂无单点节点'}
            desc={tab === 'composite' ? '点击右上角「组合节点」创建。' : tab === 'landing' ? '在「单点」或「组合」勾选后点「移入落地」。此分组与「落地仓库」无关。' : '点击右上角「添加节点」创建。'}
          />
        ) : filtered.length === 0 ? (
          <Empty title="无匹配节点" desc="可搜名称、连接 IP、线路地址或节点 ID。" />
        ) : (<>
          {!isMobile && <div ref={listRef} className="relative">
          <table className="tbl">
            <thead><tr>
              <th className="w-8">
                <input type="checkbox" className="accent-emerald-600"
                  checked={filtered.length > 0 && filtered.every(n => selected.has(n.id))}
                  onChange={toggleAll} />
              </th>
              <th>名称</th>
              <th title="用户/上游连业务端口用的 IP 或域名">线路地址</th>
              <th>版本</th>
              <th>状态</th>
              <th className="cursor-pointer select-none" onClick={() => cycleSort('traffic')}
                title="按授权记账的当期用量：单向计费节点只计上行，随用户流量周期重置清零">
                <span className="inline-flex items-center">流量<SortArrow col="traffic" sort={sort} /></span>
              </th>
              {showRawCol && <th className="cursor-pointer select-none" onClick={() => cycleSort('rawtraffic')}
                title="节点实际转发的累计字节（上行+下行），不乘倍率、不随重置清零">
                <span className="inline-flex items-center">原始流量<SortArrow col="rawtraffic" sort={sort} /></span>
              </th>}
              <th className="cursor-pointer select-none min-w-[140px]" onClick={() => cycleSort('speed')}>
                <span className="inline-flex items-center">速度<SortArrow col="speed" sort={sort} /></span>
              </th>
              <th className="text-right">操作</th>
            </tr></thead>
            <tbody>
              {filtered.map((n, i) => (
                <tr key={n.id}
                  onDragOver={draggable ? e => e.preventDefault() : undefined}
                  onDrop={draggable ? () => onDrop(i) : undefined}
                  onPointerDown={e => onPinDown(e, i)}
                  onPointerMove={onPinMove}
                  onPointerUp={onPinUp}
                  onClick={() => { if (!pinClickGuard.current) navigate(`/nodes/${n.id}`) }}
                  className={`cursor-pointer ${dragIndex === i ? 'opacity-50' : ''} ${pinMode?.idx === i ? 'opacity-40' : ''} ${selected.has(n.id) ? 'bg-emerald-50/40 dark:bg-emerald-900/10' : ''}`}>
                  <td onClick={e => e.stopPropagation()}>
                    <input type="checkbox" className="accent-emerald-600" checked={selected.has(n.id)} onChange={() => toggleOne(n.id)} />
                  </td>
                  <td>
                    <span className="inline-flex items-center gap-2 font-semibold text-emerald-600 flex-wrap">
                      {draggable && (
                        <span className="text-ink-mut select-none cursor-move font-normal" title="拖拽排序"
                          draggable onDragStart={e => { e.stopPropagation(); setDragIndex(i) }}
                          onClick={e => e.stopPropagation()}>⠿</span>
                      )}
                      <span className={`w-1.5 h-1.5 rounded-full flex-none ${!n.disabled && n.online === 1 ? 'bg-green-500 shadow-[0_0_0_3px_rgba(34,197,94,0.18)]' : 'bg-gray-400 shadow-[0_0_0_3px_rgba(154,163,176,0.16)]'}`} />
                      <span className="font-mono text-[11px] text-ink-mut font-normal">#{n.id}</span>
                      {n.name}
                      <NodeStackBadge node={n} />
                      {isLandingListGroup(n) && <Badge color="amber">落地</Badge>}
                      <NodeBillingBadges node={n} />
                    </span>
                  </td>
                  <td className="font-mono text-xs max-w-[200px]" onClick={e => e.stopPropagation()}>
                    {n.node_type === 'composite' ? (
                      <span className="text-ink-mut">—</span>
                    ) : n.relay_host ? (
                      <CopyText text={n.relay_host}>
                        <span className="text-ink-soft truncate inline-block max-w-[180px] align-bottom" title={n.relay_host}>
                          {n.relay_host}
                        </span>
                      </CopyText>
                    ) : (
                      <span className="text-amber-600 font-semibold">未设置</span>
                    )}
                  </td>
                  <td className="font-mono text-xs">
                    <span className={isAgentOutdated(n) ? 'text-red-600 font-semibold' : ''}>{displayAgentVersion(n)}</span>
                  </td>
                  <td>
                    <NodeStatus node={n} />
                    {n.node_type !== 'composite' && n.last_apply_at?.Valid && n.online === 1 && !nullStr(n.last_error) && (
                      <div className="text-[10.5px] text-ink-mut mt-0.5 font-mono">{fmtTime(n.last_apply_at.Int64)}</div>
                    )}
                  </td>
                  <td className="font-mono text-xs text-ink-mut">{fmtBytes(node_traffic[n.id] || 0)}</td>
                  {showRawCol && <td className="font-mono text-xs text-ink-mut">{fmtBytes(node_raw_traffic[n.id] || 0)}</td>}
                  <td className="font-mono text-xs whitespace-nowrap min-w-[140px]">
                    {speeds[n.id] ? (
                      <>
                        <span className="text-emerald-600">↑{fmtSpeed(speeds[n.id].up)}</span>
                        {' '}
                        <span className="text-emerald-600">↓{fmtSpeed(speeds[n.id].down)}</span>
                      </>
                    ) : (
                      <span className="text-ink-mut">—</span>
                    )}
                  </td>
                  <td className="text-right whitespace-nowrap" onClick={e => e.stopPropagation()}>
                    <div className="flex gap-1.5 justify-end">
                      {n.node_type !== 'composite' && isAgentOutdated(n) && n.online === 1 && !n.disabled && (
                        <button type="button" onClick={() => upgradeNode(n.id)} title="升级此节点" className="icon-btn text-amber-700" >
                          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 19V5"/><path d="m5 12 7-7 7 7"/></svg>
                        </button>
                      )}
                      {n.node_type !== 'composite' && <button onClick={() => resyncNode(n.id)} title="重新同步" className="icon-btn">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>
                      </button>}
                      <button onClick={() => deleteNode(n)} title="删除" className="icon-btn-danger">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><path d="M10 11v6"/><path d="M14 11v6"/></svg>
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {pinMode && (
            <div className="fixed z-50 pointer-events-none flex flex-col items-end gap-1"
              style={{ left: pinMode.cx - 16, top: pinMode.cy - 18, transform: 'translateX(-100%)' }}>
              <div className={`rounded-md px-3.5 py-1.5 text-[12px] font-bold shadow transition-colors ${
                pinMode.zone === 'top' ? 'bg-[var(--brand-soft)] text-[var(--brand-to)] border border-line' : 'bg-white/90 text-ink-soft border border-line'}`}>↑ 置顶</div>
              <div className={`rounded-md px-3.5 py-1.5 text-[12px] font-bold shadow transition-colors ${
                pinMode.zone === 'bottom' ? 'bg-amber-500 text-white' : 'bg-white/90 text-amber-400 border border-amber-200'}`}>置底 ↓</div>
            </div>
          )}
          </div>}
          {isMobile && <div>
            {filtered.map(n => (
              <div key={n.id} className="mobile-card">
                <div className="flex items-center gap-2 mb-1">
                  <input type="checkbox" className="accent-emerald-600 flex-none" checked={selected.has(n.id)} onChange={() => toggleOne(n.id)} />
                  <Link to={`/nodes/${n.id}`} className="flex-1 min-w-0 flex items-center justify-between no-underline text-ink">
                  <span className="inline-flex items-center gap-2 font-semibold text-emerald-600 flex-wrap">
                    <span className={`w-1.5 h-1.5 rounded-full flex-none ${!n.disabled && n.online === 1 ? 'bg-green-500' : 'bg-gray-400'}`} />
                    <span className="font-mono text-[11px] text-ink-mut font-normal">#{n.id}</span>
                    {n.name}
                    {isLandingListGroup(n) && <Badge color="amber">落地</Badge>}
                    <NodeBillingBadges node={n} />
                  </span>
                  <NodeStatus node={n} />
                  </Link>
                </div>
                <Link to={`/nodes/${n.id}`} className="block no-underline text-ink">
                <div className="flex items-center gap-2 text-xs text-ink-soft flex-wrap mb-1">
                  {n.relay_host
                    ? <span className="font-mono truncate max-w-full">{n.relay_host}</span>
                    : n.node_type !== 'composite' && <span className="text-amber-600">线路地址未设置</span>}
                  {isAgentOutdated(n) && <span className="text-red-600 font-mono">{displayAgentVersion(n)}</span>}
                </div>
                <div className="flex items-center gap-2 text-xs text-ink-soft flex-wrap">
                  <span className="font-mono text-ink-mut">{fmtBytes(node_traffic[n.id] || 0)}</span>
                  {speeds[n.id] && <>
                    <span className="text-ink-mut">·</span>
                    <span className="font-mono text-emerald-600">↑{fmtSpeed(speeds[n.id].up)}</span>
                    <span className="font-mono text-emerald-600">↓{fmtSpeed(speeds[n.id].down)}</span>
                  </>}
                </div>
                </Link>
              </div>
            ))}
          </div>}
        </>)}
        </TableScroll>
      </Panel>
      </div>

      <AddNodeModal open={showAdd} onClose={() => setShowAdd(false)} onDone={() => { setShowAdd(false); load() }} />
      <CompositeNodeModal open={showComposite} onClose={() => setShowComposite(false)} nodes={nodes.filter(n => n.node_type !== 'composite')} onDone={() => { setShowComposite(false); load() }} />
    </Layout>
  )
}

function SortArrow({ col, sort }) {
  const active = sort.col === col
  return (
    <span className="inline-flex flex-col leading-[0.55] text-[9px] ml-1">
      <span className={active && sort.dir === 'asc' ? 'text-emerald-600' : 'text-ink-mut opacity-50'}>▲</span>
      <span className={active && sort.dir === 'desc' ? 'text-emerald-600' : 'text-ink-mut opacity-50'}>▼</span>
    </span>
  )
}

// 打开弹窗时才拉一次用户列表；授权只对普通用户有意义，管理员天然全量可见。
function useGrantableUsers(open) {
  const [users, setUsers] = useState(null)
  useEffect(() => {
    if (!open || users) return
    api.get('/users').then(d => setUsers((d.users || []).filter(u => u.role === 'user'))).catch(() => setUsers([]))
  }, [open, users])
  return users || []
}

function GrantUsersField({ users, value, onChange }) {
  return (
    <>
      <label className="text-[13px] font-semibold text-ink-soft">授权用户 <span className="text-ink-mut font-normal text-xs">(可选)</span></label>
      <Select multiple searchable placeholder="不授权，可稍后在用户详情添加" value={value} onChange={onChange}
        options={users.map(u => ({ value: u.id, label: u.username }))} />
    </>
  )
}

function AddNodeModal({ open, onClose, onDone }) {
  const [name, setName] = useState('')
  const [secret, setSecret] = useState('')
  const [portStart, setPortStart] = useState('10001')
  const [portEnd, setPortEnd] = useState('20000')
  const [rateMult, setRateMult] = useState('1')
  const [unidirectional, setUnidirectional] = useState(false)
  const [userIds, setUserIds] = useState([])
  const [loading, setLoading] = useState(false)
  // Holds the freshly created node's one-time plaintext secret + id. While set,
  // a reveal modal shows the token; the caller navigates on to detail only after
  // the operator dismisses it, since the token can never be read back later.
  const [created, setCreated] = useState(null)
  const users = useGrantableUsers(open)
  const toast = useToast()
  const navigate = useNavigate()

  const submit = async (e) => {
    e.preventDefault()
    setLoading(true)
    try {
      const portRange = `${portStart || '10001'}-${portEnd || '20000'}`
      const rm = parseFloat(rateMult)
      const res = await api.post('/nodes', {
        name, secret: secret || undefined, port_range: portRange, rate_multiplier: rm >= 0 ? rm : 1, unidirectional,
        user_ids: userIds.length ? userIds.map(Number) : undefined,
      })
      toast('节点已添加')
      setName(''); setSecret(''); setPortStart('10001'); setPortEnd('20000'); setRateMult('1'); setUnidirectional(false); setUserIds([])
      if (res?.secret && res?.node?.id) setCreated({ id: res.node.id, secret: res.secret })
      else if (res?.node?.id) navigate(`/nodes/${res.node.id}`)
      else onDone()
    } catch (err) { toast(err.message, 'error') } finally { setLoading(false) }
  }

  if (created) {
    return (
      <Modal open={open} onClose={() => { const cid = created.id; setCreated(null); navigate(`/nodes/${cid}`) }} title="节点已创建 · Token">
        <div className="text-[13px] text-ink-soft mb-3">这是该节点的 Token，可点击复制。在节点详情页也能随时查看它和完整安装命令。</div>
        <div className="rounded-[10px] border border-line bg-[#f7f9fc] px-3.5 py-3 font-mono text-[13px] break-all">
          <CopyText text={created.secret} />
        </div>
        <div className="flex justify-end mt-5">
          <button onClick={() => { const cid = created.id; setCreated(null); navigate(`/nodes/${cid}`) }} className="btn-primary px-5">我已保存，前往详情</button>
        </div>
      </Modal>
    )
  }

  return (
    <Modal open={open} onClose={onClose} title="添加节点">
      <form onSubmit={submit} className="space-y-4">
        <div className="grid grid-cols-[140px_1fr] gap-4 items-center">
          <label className="text-[13px] font-semibold text-ink-soft">名称</label>
          <input className="input-field" value={name} onChange={e => setName(e.target.value)} required placeholder="例如 hk-1" />
          <label className="text-[13px] font-semibold text-ink-soft">Token <span className="text-ink-mut font-normal text-xs">(可选)</span></label>
          <input className="input-field font-mono" value={secret} onChange={e => setSecret(e.target.value)} placeholder="留空则随机生成 64 位 hex" />
          <label className="text-[13px] font-semibold text-ink-soft">倍率</label>
          <input className="input-field font-mono" type="number" min="0" step="0.1" value={rateMult} onChange={e => setRateMult(e.target.value)} style={{ width: 100 }} />
          <label className="text-[13px] font-semibold text-ink-soft">计费方向</label>
          <div className="flex items-center gap-2">
            <button type="button" onClick={() => setUnidirectional(u => !u)}
              className={`inline-flex items-center gap-1.5 px-3.5 py-[7px] rounded-[8px] text-[13px] font-semibold border cursor-pointer transition-colors ${unidirectional ? 'bg-amber-50 text-amber-700 border-amber-200 hover:bg-amber-100' : 'bg-emerald-50 text-emerald-700 border-emerald-200 hover:bg-emerald-100'}`}>
              {unidirectional ? '单向计费（仅出站）' : '双向计费（出站+入站）'}
            </button>
            <span className="text-xs text-ink-mut">当前：{unidirectional ? '单向' : '双向'}</span>
          </div>
          <GrantUsersField users={users} value={userIds} onChange={setUserIds} />
        </div>
        <div className="flex gap-2">
          <div className="flex-1">
            <label className="text-[13px] font-semibold text-ink-soft block mb-1">起始端口</label>
            <input className="input-field w-full font-mono" type="number" min="1" max="65535"
              value={portStart} onChange={e => setPortStart(e.target.value)} placeholder="10001" />
          </div>
          <div className="flex-1">
            <label className="text-[13px] font-semibold text-ink-soft block mb-1">结束端口</label>
            <input className="input-field w-full font-mono" type="number" min="1" max="65535"
              value={portEnd} onChange={e => setPortEnd(e.target.value)} placeholder="20000" />
          </div>
        </div>
        <div className="flex gap-3 pt-4 border-t border-line-soft">
          <button type="submit" disabled={loading} className="btn-primary">添加节点</button>
          <button type="button" onClick={onClose} className="btn-secondary">取消</button>
          <span className="text-xs text-ink-mut ml-auto">添加后会生成 token 与安装命令。</span>
        </div>
      </form>
    </Modal>
  )
}

function NodeStatus({ node }) {
  if (node.disabled) return <Badge color="amber">禁用</Badge>
  // A composite node has no agent of its own to sync; its health is the
  // aggregate of its child hops, so show online/offline rather than a sync
  // state that would always read as "pending" or surface a spurious error.
  if (node.node_type === 'composite') {
    return node.online === 1 ? <Badge color="green">在线</Badge> : <Badge color="gray">离线</Badge>
  }
  // Error/warning outrank connectivity: they explain why the node needs
  // attention, so they stay visible while offline instead of being hidden
  // behind a silent "离线" — matches the detail page header's priority
  // (disabled > error > warning > online > offline).
  const lastErr = nullStr(node.last_error)
  if (lastErr) return <Badge color="red" title={lastErr}>错误</Badge>
  if (node.last_warning) return <Badge color="amber" title={node.last_warning}>警告</Badge>
  // A disconnected agent is offline regardless of when it last synced; a stale
  // "已同步" on an offline node misrepresents its real state.
  if (node.online !== 1) return <Badge color="gray">离线</Badge>
  if (node.last_apply_at?.Valid) return <Badge color="green">已同步</Badge>
  return <Badge color="amber">待同步</Badge>
}

function CompositeNodeModal({ open, onClose, nodes, onDone }) {
  const [name, setName] = useState('')
  const [rateMult, setRateMult] = useState('1')
  const [hops, setHops] = useState([{ node_id: '', mode: 'userspace' }])
  const [userIds, setUserIds] = useState([])
  const [loading, setLoading] = useState(false)
  const users = useGrantableUsers(open)
  const toast = useToast()
  const navigate = useNavigate()

  const addHop = () => setHops(h => [...h, { node_id: '', mode: 'userspace' }])
  const removeHop = (i) => setHops(h => h.filter((_, j) => j !== i))
  const setHop = (i, k, v) => setHops(h => h.map((hop, j) => j === i ? { ...hop, [k]: v } : hop))
  const moveHop = (i, dir) => {
    setHops(h => {
      const arr = [...h]
      const j = i + dir
      if (j < 0 || j >= arr.length) return arr
      ;[arr[i], arr[j]] = [arr[j], arr[i]]
      return arr
    })
  }

  const submit = async (e) => {
    e.preventDefault()
    // 空行必须显式处理而不是静默过滤：过滤会让末跳与界面按行数标出的末跳
    // 错位，末跳模式（中间层生效、出口被覆盖）会落到错误的行上
    if (hops.some(h => !h.node_id)) {
      toast('请为每一跳选择节点，或删除空行', 'error')
      return
    }
    if (hops.length < 2) {
      toast('组合节点至少需要 2 个子节点', 'error')
      return
    }
    setLoading(true)
    try {
      const rm = parseFloat(rateMult)
      const res = await api.post('/nodes', {
        name,
        node_type: 'composite',
        rate_multiplier: rm >= 0 ? rm : 1,
        hops: hops.map(h => ({ node_id: Number(h.node_id), mode: h.mode })),
        user_ids: userIds.length ? userIds.map(Number) : undefined,
      })
      toast('组合节点已创建')
      setName('')
      setRateMult('1')
      setHops([{ node_id: '', mode: 'userspace' }])
      setUserIds([])
      if (res?.node?.id) navigate(`/nodes/${res.node.id}`)
      else onDone()
    } catch (err) { toast(err.message, 'error') } finally { setLoading(false) }
  }

  return (
    <Modal open={open} onClose={onClose} title="创建组合节点">
      <form onSubmit={submit} className="space-y-4">
        <div className="grid grid-cols-[140px_1fr] gap-4 items-center">
          <label className="text-[13px] font-semibold text-ink-soft">名称</label>
          <input className="input-field" value={name} onChange={e => setName(e.target.value)} required placeholder="例如 hk-jp-chain" />
          <label className="text-[13px] font-semibold text-ink-soft">倍率</label>
          <input className="input-field font-mono" type="number" min="0" step="0.1" value={rateMult} onChange={e => setRateMult(e.target.value)} style={{ width: 100 }} />
          <GrantUsersField users={users} value={userIds} onChange={setUserIds} />
        </div>

        <div>
          <div className="flex items-center gap-2 mb-2">
            <span className="text-[13px] font-semibold text-ink-soft">跳序（从入口到出口）</span>
          </div>
          <div className="space-y-2">
            {hops.map((hop, i) => (
              <div key={i} className="flex items-center gap-2 bg-raised rounded-lg px-3 py-2">
                <span className="text-xs text-ink-mut w-5 text-center font-mono">{i + 1}</span>
                <Select className="flex-1" placeholder="-- 选择节点 --" searchable value={hop.node_id} onChange={v => setHop(i, 'node_id', v)}
                  options={nodes.filter(n => n.id === Number(hop.node_id) || !hops.some((h, j) => j !== i && Number(h.node_id) === n.id)).map(n => ({ value: n.id, label: n.name }))} />
                {/* 每一跳都可配模式，包含末跳：末跳模式在该组合被用作中间层时生效；
                    被用作规则出口时由规则的出口模式覆盖 */}
                <Select value={hop.mode} onChange={v => setHop(i, 'mode', v)} style={{ width: 110 }}
                  title={i === hops.length - 1 ? '末跳模式：作为中间层时生效；作为规则出口时由规则的出口模式覆盖' : undefined}
                  options={[{ value: 'kernel', label: 'kernel' }, { value: 'userspace', label: 'userspace' }]} />
                {i === hops.length - 1 && (
                  <span className="text-[11px] text-ink-mut shrink-0 cursor-help" title="末跳模式：作为中间层时生效；作为规则出口时由规则的出口模式覆盖">末</span>
                )}
                <button type="button" onClick={() => moveHop(i, -1)} disabled={i === 0} className="btn-secondary text-xs px-1.5">↑</button>
                <button type="button" onClick={() => moveHop(i, 1)} disabled={i === hops.length - 1} className="btn-secondary text-xs px-1.5">↓</button>
                {hops.length > 1 && (
                  <button type="button" onClick={() => removeHop(i)} className="btn-danger-sm text-xs px-1.5">×</button>
                )}
              </div>
            ))}
          </div>
          <button type="button" onClick={addHop} className="btn-secondary text-xs mt-2">+ 添加一跳</button>
        </div>

        <div className="flex gap-3 pt-4 border-t border-line-soft">
          <button type="submit" disabled={loading} className="btn-primary">创建组合节点</button>
          <button type="button" onClick={onClose} className="btn-secondary">取消</button>
        </div>
      </form>
    </Modal>
  )
}
