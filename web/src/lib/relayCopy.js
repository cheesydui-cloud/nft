/* Shared clipboard formatting for rewritten relay proxy URIs.
   List UI keeps landing node names; only copy/export renames to
   `{username}-8月5日` or `{username}-{ruleName}`. */

import { uriToClashYaml } from './yaml-convert'
import {
  allocateRelayDisplayNames,
  buildRelayDisplayName,
  renameRelayURI,
  setURIName,
} from './landing'

function exitKey(host, port) {
  if (!host || !port) return ''
  return `${host}:${port}`
}

export function relayExpiryFromMap(expiryMap, host, port) {
  if (!expiryMap) return 0
  const k = exitKey(host, port)
  if (!k) return 0
  return expiryMap.get(k) || 0
}

/** Format one relay URI (or fall back to raw entry host:port). */
export function formatRelayCopyText(uri, {
  username,
  ruleName,
  expiresAt,
  displayName,
  asYaml = false,
} = {}) {
  if (!uri) return null
  const name = displayName || buildRelayDisplayName({ username, ruleName, expiresAt })
  const renamed = name ? (setURIName(uri, name) || uri) : uri
  if (asYaml) {
    const yaml = uriToClashYaml(renamed)
    if (yaml) return yaml
  }
  return renamed
}

/**
 * Build clipboard text for a rule's client-facing links.
 * Prefer renamed relay URIs; fall back to entry host:port (unchanged).
 */
export function formatRuleCopyParts(rule, {
  username,
  expiryMap,
  asYaml = false,
  displayName,
} = {}) {
  const expiresAt = relayExpiryFromMap(expiryMap, rule?.exit_host, rule?.exit_port)
  const opts = {
    username: username || rule?.owner_name || '',
    ruleName: rule?.name || '',
    expiresAt,
    displayName,
    asYaml,
  }
  const parts = []
  if (rule?.relay_uri) {
    parts.push(formatRelayCopyText(rule.relay_uri, opts))
  }
  if (rule?.relay_uri_v6) {
    // Same display name for dual-stack pair; Clash may still need unique names
    // when both are imported — callers that batch should allocate.
    const v6Name = opts.displayName
      ? `${opts.displayName}-v6`
      : (buildRelayDisplayName(opts) ? `${buildRelayDisplayName(opts)}-v6` : '')
    parts.push(formatRelayCopyText(rule.relay_uri_v6, { ...opts, displayName: v6Name || undefined }))
  }
  if (!parts.length) {
    if (rule?.entry) parts.push(rule.entry)
    if (rule?.entry_v6) parts.push(rule.entry_v6)
  }
  return parts.filter(Boolean)
}

export function formatRuleCopyText(rule, opts = {}) {
  return formatRuleCopyParts(rule, opts).join('\n').trim()
}

/** Batch-rename relay URIs with unique names (for 复制全部). */
export function formatRelayBatch(items, { asYaml = false } = {}) {
  const alloc = allocateRelayDisplayNames(items.map((it, i) => ({
    key: it.key ?? i,
    username: it.username,
    ruleName: it.ruleName,
    expiresAt: it.expiresAt,
  })))
  return items.map((it, i) => {
    const key = it.key ?? i
    const name = alloc.get(key)
    const uri = it.uri
    if (!uri) return null
    return formatRelayCopyText(uri, { displayName: name, asYaml })
  }).filter(Boolean)
}

export { renameRelayURI, buildRelayDisplayName, setURIName }
