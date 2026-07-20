import { useState, useEffect, useMemo } from 'react'
import { api } from '../../lib/api'
import { Layout, useToast, useBlur, useUser } from '../../components/Layout'
import { Loading, Empty, CopyText, SensText, Badge } from '../../components/ui'
import { PageHeader, Panel, PanelToolbar, SearchInput, TableScroll } from '../../components/page'
import { parseURIs, mergeLanding, loadLocalURIs, fetchNodeRoles, loadLocalRoles, nodeHasRole, ROLE_LANDING } from '../../lib/landing'
import { fmtDate, expiryBadge } from '../../lib/fmt'

/* Landing-nodes nav: lists the nodes available to the user — the admin-assigned
   ones (resolved server-side from a subscription and/or URIs) plus the user's
   own browser-local URIs — each with a one-click copy of its original (direct)
   proxy URI. The user's own URIs win on a host:port collision. The refresh
   button appears only for a dynamic source (a subscription URL). */
export default function MyLandingNodes() {
  const [serverNodes, setServerNodes] = useState(null)
  const [roles, setRoles] = useState(null)
  const [hasDynamic, setHasDynamic] = useState(false)
  const [stale, setStale] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [search, setSearch] = useState('')
  const toast = useToast()
  const blurred = useBlur()
  const { user } = useUser()

  const localNodes = useMemo(
    () => parseURIs(loadLocalURIs(user?.username)).map(n => ({ ...n, source: 'local' })),
    [user])

  const load = (refresh = false) => {
    if (refresh) setRefreshing(true)
    api.get(`/my/landing-nodes${refresh ? '?refresh=1' : ''}`)
      .then(d => {
        setServerNodes((d?.nodes || []).map(n => ({ ...n, source: 'admin' })))
        setHasDynamic(!!d?.has_dynamic)
        setStale(!!d?.stale)
      })
      .catch(console.error)
      .finally(() => setRefreshing(false))
  }
  useEffect(() => {
    load(false)
    fetchNodeRoles().then(sr => setRoles({ ...sr, ...loadLocalRoles(user?.username) }))
  }, [])

  if (serverNodes === null || roles === null) return <Layout><Loading /></Layout>

  const refresh = () => { load(true); toast('已刷新订阅') }

  /* Quotas are enforced per host:port regardless of which URI won the merge,
     so the ledger lookup must not depend on the row's source: a user pasting
     their own copy of an admin-assigned node keeps seeing its quota. */
  const ledger = new Map((serverNodes || []).map(n => [`${n.host}:${n.port}`, n]))

  const allNodes = mergeLanding(localNodes, serverNodes)
  const nodes = allNodes.filter(n => nodeHasRole(roles, n, ROLE_LANDING))

  const q = search.trim().toLowerCase()
  const filtered = !q ? nodes : nodes.filter(n =>
    [n.name, n.protocol, `${n.host}:${n.port}`].some(v => (v || '').toLowerCase().includes(q)))

  return (
    <Layout>
      <div className="h-full flex flex-col">
      <PageHeader title="落地节点" count={nodes.length} unit="个" />
      <Panel fill>
        <PanelToolbar>
          <SearchInput value={search} onChange={setSearch} placeholder="搜索名称、协议、地址…" />
          {stale && <span className="text-xs text-amber-600 ml-2">订阅刷新失败，显示上次结果</span>}
          {hasDynamic && (
            <button onClick={refresh} disabled={refreshing}
              className="ml-auto inline-flex items-center gap-1.5 text-[13.5px] font-semibold text-ink-soft bg-surface border border-line hover:border-emerald-500 hover:text-emerald-600 px-[18px] py-[9px] rounded-[10px] transition-colors disabled:opacity-50">
              {refreshing ? '刷新中…' : '刷新订阅'}
            </button>
          )}
        </PanelToolbar>

        <TableScroll>
        {nodes.length === 0 ? (
          <Empty title="暂无落地节点" desc="请联系管理员分配落地节点，或由管理员为你配置订阅来源。" />
        ) : filtered.length === 0 ? (
          <Empty title="无匹配节点" desc="试试别的关键词。" />
        ) : (
          <table className="tbl">
            <thead><tr><th>名称</th><th>协议</th><th>地址</th><th>到期时间</th><th>来源</th><th className="text-right">操作</th></tr></thead>
            <tbody>
              {filtered.map((n, i) => (
                <tr key={i}>
                  <td className="font-semibold">{n.name || '(未命名)'}</td>
                  <td className="font-mono text-xs text-ink-soft">{n.protocol}</td>
                  <td className="font-mono text-xs"><SensText blurred={blurred}>{n.host}:{n.port}</SensText></td>
                  <td className="text-xs">
                    {(() => {
                      const ex = ledger.get(`${n.host}:${n.port}`)
                      if (!ex || !ex.expires_at || ex.expires_at <= 0) return <span className="text-ink-mut">—</span>
                      const badge = expiryBadge(ex.expires_at)
                      return (
                        <>
                          {fmtDate(ex.expires_at)}
                          {badge && <Badge color={badge.color} className="ml-1">{badge.label}</Badge>}
                        </>
                      )
                    })()}
                  </td>
                  <td>{n.source === 'local' ? <Badge color="blue">本地</Badge> : <Badge color="gray">分配</Badge>}</td>
                  <td className="text-right">
                    <CopyText text={`${n.host}:${n.port}`}><span className="text-emerald-600 font-sans text-xs font-semibold">复制</span></CopyText>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        </TableScroll>
      </Panel>
      </div>
    </Layout>
  )
}
