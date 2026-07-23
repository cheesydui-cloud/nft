import { useState, useEffect, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../../lib/api'
import { Layout, useToast, useBlur, useUser, useCopyFmt } from '../../components/Layout'
import { Loading, Empty, useConfirm } from '../../components/ui'
import { PageHeader, Panel, PanelToolbar, SearchInput, ToolbarButton, ToolbarActions, TableScroll } from '../../components/page'
import { RulesTable } from '../../components/RulesTable'
import { RuleFormModal, ruleFormToPayload } from '../../components/RuleFormModal'
import { parseURIs, landingIndex, mergeLanding, loadLocalURIs, saveLocalURIs, loadSubCache, fetchNodeRoles, loadLocalRoles, nodeHasRole, ROLE_LANDING, enrichRuleWithLanding } from '../../lib/landing'
import { copyToClipboard } from '../../lib/clipboard'
import { formatRuleCopyText } from '../../lib/relayCopy'

export default function MyRules() {
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [serverLanding, setServerLanding] = useState([])
  const [createOpen, setCreateOpen] = useState(false)
  const [createInitial, setCreateInitial] = useState(null)
  const [search, setSearch] = useState('')
  const [probeAllTrigger, setProbeAllTrigger] = useState(0)
  const navigate = useNavigate()
  const toast = useToast()
  const blurred = useBlur()
  const confirm = useConfirm()
  const { user } = useUser()
  const { copyFmt } = useCopyFmt()

  // The user's own proxy URIs live only in localStorage (never sent to the
  // server). Parse them here to both feed the create picker and resolve a
  // client-side relay URI for rules whose exit matches one of them.
  const [localVer, setLocalVer] = useState(0)
  const [nodeRoles, setNodeRoles] = useState({})
  useEffect(() => {
    fetchNodeRoles().then(sr => setNodeRoles({ ...sr, ...loadLocalRoles(user?.username) }))
  }, [user])
  const localNodes = useMemo(() => {
    const isLanding = n => nodeHasRole(nodeRoles, n, ROLE_LANDING)
    const manual = parseURIs(loadLocalURIs(user?.username)).filter(isLanding)
    const sub = loadSubCache(user?.username).filter(isLanding)
    return mergeLanding(manual, sub)
  }, [user, localVer, nodeRoles])
  const localIdx = useMemo(() => landingIndex(localNodes), [localNodes])

  const addProxyURI = (uri) => {
    if (!user?.username) return
    const existing = loadLocalURIs(user.username)
    const lines = existing.split('\n').map(l => l.trim()).filter(Boolean)
    if (lines.includes(uri.trim())) return
    lines.push(uri.trim())
    saveLocalURIs(user.username, lines.join('\n'))
    setLocalVer(v => v + 1)
  }

  const load = () => {
    setLoading(true)
    api.get('/my/rules').then(setData).catch(console.error).finally(() => setLoading(false))
    api.get('/my/landing-nodes').then(d => setServerLanding(d?.nodes || [])).catch(console.error)
  }
  useEffect(load, [])

  // Build expiry lookup from server landing nodes (includes expires_at).
  // Must be before any conditional return to respect Hooks rules.
  const landingExpiry = useMemo(() => {
    const m = new Map()
    ;(serverLanding || []).forEach(n => {
      if (n.expires_at > 0) m.set(`${n.host}:${n.port}`, n.expires_at)
    })
    return m
  }, [serverLanding])

  if (loading) return <Layout><Loading /></Layout>

  const { rules = [], nodes = [], node_by_id = {}, show_rate, bindings = [] } = data || {}

  // Filter server-assigned nodes by global role table — only landing-marked ones
  // appear in the exit picker (unconfigured/direct ones are excluded).
  const serverLandingFiltered = serverLanding.filter(n => nodeHasRole(nodeRoles, n, ROLE_LANDING))
  const landingNodes = mergeLanding(localNodes, serverLandingFiltered)

  const allLandingIdx = landingIndex(landingNodes)

  const enrich = (r) => enrichRuleWithLanding(r, allLandingIdx)

  const deleteRule = async (rule) => {
    if (!(await confirm({ title: '删除规则', message: `确认删除规则「${rule.name}」？`, confirmText: '删除', danger: true }))) return
    try { await api.del(`/my/rules/${rule.id}`); toast('已删除'); load() } catch (err) { toast(err.message, 'error') }
  }
  const openCreate = () => { setCreateInitial(null); setCreateOpen(true) }
  const copyRule = async (rule) => {
    const text = formatRuleCopyText(rule, {
      username: user?.username,
      expiryMap: landingExpiry,
      asYaml: copyFmt === 'yaml',
    })
    if (!text) {
      toast('该规则没有可复制的链接', 'error')
      return
    }
    try {
      await copyToClipboard(text)
      toast('规则链接已复制')
    } catch {
      toast('复制失败', 'error')
    }
  }

  const q = search.trim().toLowerCase()
  const enriched = rules.map(enrich)
  const filtered = !q ? enriched : enriched.filter(r => {
    const node = node_by_id?.[r.node_id]
    const exit = r.exit_host && r.exit_port ? `${r.exit_host}:${r.exit_port}` : ''
    return [r.name, node?.name, r.entry, exit].some(v => (v || '').toLowerCase().includes(q))
  })

  return (
    <Layout>
      <div className="h-full flex flex-col">
      <PageHeader title="我的规则" count={rules.length} />

      <Panel fill>
        <PanelToolbar>
          <SearchInput value={search} onChange={setSearch} placeholder="搜索规则名称、节点、目标…" />
          <ToolbarActions>
            <ToolbarButton onClick={() => setProbeAllTrigger(t => t + 1)} secondary>测试所有</ToolbarButton>
            <ToolbarButton onClick={openCreate}>＋ 创建规则</ToolbarButton>
          </ToolbarActions>
        </PanelToolbar>

        {rules.length === 0 ? (
          <Empty title="暂无规则" desc="点击右上角「创建规则」开始。" />
        ) : filtered.length === 0 ? (
          <Empty title="无匹配规则" desc="试试别的关键词。" />
        ) : (
          <TableScroll>
            <RulesTable variant="my" rules={filtered} nodeMap={node_by_id} blurred={blurred}
              onDelete={deleteRule} onCopy={copyRule} onRowClick={r => navigate(`/my/rules/${r.id}`)}
              probeAllTrigger={probeAllTrigger} displayRate={user?.billing_rate ?? 1} landingExpiry={landingExpiry}
              copyUsername={user?.username || ''} />
          </TableScroll>
        )}
      </Panel>
      </div>

      <RuleFormModal
        open={createOpen} onClose={() => setCreateOpen(false)} title="创建规则" submitLabel="创建规则"
        nodes={nodes} landingNodes={landingNodes} bindings={bindings} initial={createInitial} onAddProxyURI={addProxyURI} showRate={show_rate}
        onSubmit={async (form) => {
          const res = await api.post('/my/rules', ruleFormToPayload(form))
          toast('规则已创建'); setCreateOpen(false)
          if (res?.rule_id) navigate(`/my/rules/${res.rule_id}`)
        }} />

    </Layout>
  )
}
