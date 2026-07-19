import { useState, useEffect, useRef } from 'react'

// sameSpeeds reports whether two speed maps carry the same up/down values, so
// the hook can skip a re-render when nothing changed (the server pushes every
// second even while every node is idle).
function sameSpeeds(a, b) {
  const ak = Object.keys(a), bk = Object.keys(b)
  if (ak.length !== bk.length) return false
  for (const k of bk) {
    const x = a[k], y = b[k]
    if (!x || x.up !== y.up || x.down !== y.down) return false
  }
  return true
}

// Shared WS so node-level and rule-level consumers share one connection.
let shared = null

function ensureShared() {
  if (shared) return shared
  const listeners = new Set()
  let speeds = {}
  let ruleSpeeds = {}
  let ws = null
  let reconnectTimer = null
  let delay = 3000
  const initialDelay = 3000
  const maxDelay = 30000
  let visible = typeof document !== 'undefined' ? !document.hidden : true
  let refCount = 0

  function publish() {
    for (const fn of listeners) fn({ speeds, ruleSpeeds })
  }

  function scheduleReconnect() {
    if (refCount === 0) return
    reconnectTimer = setTimeout(() => {
      delay = Math.min(delay * 2, maxDelay)
      connect()
    }, visible ? delay : maxDelay)
  }

  function connect() {
    if (refCount === 0) return
    try {
      if (ws) {
        ws.onclose = null
        ws.onerror = null
        ws.onmessage = null
        try { ws.close() } catch {}
      }
    } catch {}
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    ws = new WebSocket(proto + '//' + location.host + '/api/ws/speed')

    ws.onopen = () => { delay = initialDelay }

    ws.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data)
        let changed = false
        if (data.speeds) {
          const map = {}
          for (const s of data.speeds) map[s.node_id] = s
          if (!sameSpeeds(speeds, map)) {
            speeds = map
            changed = true
          }
        }
        if (data.rule_speeds) {
          const map = {}
          for (const s of data.rule_speeds) map[s.rule_id] = s
          if (!sameSpeeds(ruleSpeeds, map)) {
            ruleSpeeds = map
            changed = true
          }
        }
        if (changed) publish()
      } catch {}
    }

    ws.onclose = () => scheduleReconnect()
    ws.onerror = () => { try { ws.close() } catch {} }
  }

  function visibilityHandler() {
    visible = !document.hidden
    if (visible && reconnectTimer) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
      connect()
    }
  }

  shared = {
    subscribe(fn) {
      listeners.add(fn)
      refCount++
      if (refCount === 1) {
        if (typeof document !== 'undefined') {
          document.addEventListener('visibilitychange', visibilityHandler)
        }
        connect()
      }
      fn({ speeds, ruleSpeeds })
      return () => {
        listeners.delete(fn)
        refCount--
        if (refCount === 0) {
          if (typeof document !== 'undefined') {
            document.removeEventListener('visibilitychange', visibilityHandler)
          }
          clearTimeout(reconnectTimer)
          reconnectTimer = null
          if (ws) {
            try { ws.close() } catch {}
            ws = null
          }
          speeds = {}
          ruleSpeeds = {}
          shared = null
        }
      }
    },
  }
  return shared
}

export function useSpeed() {
  const [speeds, setSpeeds] = useState({})
  useEffect(() => ensureShared().subscribe(({ speeds: s }) => setSpeeds(s)), [])
  return speeds
}

// Per-rule live rates keyed by rule_id. Prefer this on any rules table; the
// node-level map misses composite rules (logical node_id never reports).
export function useRuleSpeed() {
  const [ruleSpeeds, setRuleSpeeds] = useState({})
  useEffect(() => ensureShared().subscribe(({ ruleSpeeds: s }) => setRuleSpeeds(s)), [])
  return ruleSpeeds
}

export function fmtSpeed(bps) {
  if (!bps || bps <= 0) return '0 B/s'
  if (bps < 1024) return bps.toFixed(0) + ' B/s'
  if (bps < 1048576) return (bps / 1024).toFixed(1) + ' KB/s'
  if (bps < 1073741824) return (bps / 1048576).toFixed(2) + ' MB/s'
  return (bps / 1073741824).toFixed(2) + ' GB/s'
}
