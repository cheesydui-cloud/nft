/* Route-line helpers for 单点 / 组合 / 代理 (proxy-service deployed nodes). */

/** Collect distinct node_ids that have at least one proxy_service instance. */
export function collectDeployedNodeIds(services = []) {
  const ids = new Set()
  for (const s of services || []) {
    for (const id of s.deployed_node_ids || []) {
      if (id > 0) ids.add(Number(id))
    }
    for (const inst of s.instances || []) {
      if (inst?.node_id > 0) ids.add(Number(inst.node_id))
    }
  }
  return ids
}

/** Fetch proxy-services list and return Set of deployed node ids. Soft-fails to empty. */
export async function loadProxyServiceNodeIds(api) {
  try {
    const d = await api.get('/proxy-services')
    return collectDeployedNodeIds(d?.services || [])
  } catch {
    return new Set()
  }
}

export function isLandingListGroup(n) {
  return (n?.list_group || '') === 'landing'
}

/**
 * Partition nodes for list chips / Select groups.
 * - single: non-composite, not in 落地 bucket
 * - composite: composite, not in 落地 bucket
 * - landing: list_group === 'landing' (manual bucket on node list)
 * - proxy: nodes that host proxy-service instances (picker tab only)
 */
export function partitionRouteNodes(nodes = [], proxyNodeIds = new Set()) {
  const list = Array.isArray(nodes) ? nodes : []
  const single = []
  const composite = []
  const landing = []
  const proxy = []
  for (const n of list) {
    if (isLandingListGroup(n)) {
      landing.push(n)
    } else if (n.node_type === 'composite') {
      composite.push(n)
    } else {
      single.push(n)
    }
    if (proxyNodeIds.has(Number(n.id))) {
      proxy.push(n)
    }
  }
  return { single, composite, landing, proxy }
}
