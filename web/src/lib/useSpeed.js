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

export function useSpeed() {
  const [speeds, setSpeeds] = useState({})
  const wsRef = useRef(null)
  const speedsRef = useRef({})

  useEffect(() => {
    let unmounted = false
    let reconnectTimer = null
    let delay = 3000
    const initialDelay = 3000
    const maxDelay = 30000
    let visible = !document.hidden

    function scheduleReconnect() {
      if (unmounted) return
      reconnectTimer = setTimeout(() => {
        delay = Math.min(delay * 2, maxDelay)
        connect()
      }, visible ? delay : maxDelay)
    }

    function connect() {
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
      const ws = new WebSocket(proto + '//' + location.host + '/api/ws/speed')
      wsRef.current = ws

      ws.onopen = () => { delay = initialDelay }

      ws.onmessage = (e) => {
        try {
          const data = JSON.parse(e.data)
          if (data.speeds) {
            const map = {}
            for (const s of data.speeds) map[s.node_id] = s
            if (!sameSpeeds(speedsRef.current, map)) {
              speedsRef.current = map
              setSpeeds(map)
            }
          }
        } catch {}
      }

      ws.onclose = () => scheduleReconnect()

      ws.onerror = () => ws.close()
    }

    const visibilityHandler = () => {
      visible = !document.hidden
      if (visible && reconnectTimer) {
        clearTimeout(reconnectTimer)
        reconnectTimer = null
        connect()
      }
    }
    document.addEventListener('visibilitychange', visibilityHandler)

    connect()

    return () => {
      unmounted = true
      document.removeEventListener('visibilitychange', visibilityHandler)
      clearTimeout(reconnectTimer)
      if (wsRef.current) wsRef.current.close()
    }
  }, [])

  return speeds
}

export function fmtSpeed(bps) {
  if (!bps || bps <= 0) return '0 B/s'
  if (bps < 1024) return bps.toFixed(0) + ' B/s'
  if (bps < 1048576) return (bps / 1024).toFixed(1) + ' KB/s'
  if (bps < 1073741824) return (bps / 1048576).toFixed(2) + ' MB/s'
  return (bps / 1073741824).toFixed(2) + ' GB/s'
}
