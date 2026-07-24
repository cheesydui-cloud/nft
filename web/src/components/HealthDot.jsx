import { healthDotClass, healthTextClass, linkHealth } from '../lib/linkHealth'

/** Compact green/yellow/red health indicator for nodes or probe results. */
export function HealthDot({ online, disabled, probeOk = null, latencyMs = null, showLabel = true, className = '' }) {
  const h = linkHealth({ online, disabled, probeOk, latencyMs })
  return (
    <span className={`inline-flex items-center gap-1.5 ${className}`} title={h.title}>
      <span className={`w-2 h-2 rounded-full flex-none ${healthDotClass(h.tone)}`} />
      {showLabel && (
        <span className={`text-[11px] font-semibold ${healthTextClass(h.tone)}`}>{h.label}</span>
      )}
    </span>
  )
}
