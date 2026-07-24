/* Link health color from node online + probe result (latency). */

/** @typedef {'unknown'|'green'|'yellow'|'red'} HealthTone */

/**
 * Map probe / node state to green / yellow / red.
 * @param {{ online?: number|boolean, disabled?: boolean, probeOk?: boolean|null, latencyMs?: number|null }} s
 * @returns {{ tone: HealthTone, label: string, title: string }}
 */
export function linkHealth({ online, disabled, probeOk = null, latencyMs = null } = {}) {
  if (disabled) {
    return { tone: 'red', label: '禁用', title: '节点已禁用' }
  }
  if (probeOk === false) {
    return { tone: 'red', label: '不通', title: '链路探测失败' }
  }
  if (probeOk === true) {
    const ms = latencyMs == null ? 0 : Number(latencyMs)
    if (ms > 300) {
      return { tone: 'yellow', label: `${ms}ms`, title: `探测成功，延迟偏高 ${ms}ms` }
    }
    return { tone: 'green', label: ms > 0 ? `${ms}ms` : '正常', title: ms > 0 ? `探测成功 ${ms}ms` : '探测成功' }
  }
  // No probe yet — fall back to node online.
  if (online === 1 || online === true) {
    return { tone: 'green', label: '在线', title: '节点在线（未探测）' }
  }
  if (online === 0 || online === false) {
    return { tone: 'red', label: '离线', title: '节点离线' }
  }
  return { tone: 'unknown', label: '—', title: '状态未知' }
}

export function healthDotClass(tone) {
  switch (tone) {
    case 'green': return 'bg-emerald-500'
    case 'yellow': return 'bg-amber-400'
    case 'red': return 'bg-red-500'
    default: return 'bg-ink-mut/40'
  }
}

export function healthTextClass(tone) {
  switch (tone) {
    case 'green': return 'text-emerald-600'
    case 'yellow': return 'text-amber-600'
    case 'red': return 'text-red-600'
    default: return 'text-ink-mut'
  }
}
