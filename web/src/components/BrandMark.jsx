// Triangle of three bidirectional arrows — panel brand mark (scheme A).
// Sized to the golden ratio relative to the 42px badge:
//   badge : mark ≈ φ (1.618) → mark ≈ 26px in a 42px box (viewBox scaled).
// Arrow stroke and radius keep white glyphs large enough to read at 42px.
export function BrandMark({ className = 'w-[26px] h-[26px]' }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.15" strokeLinecap="round" strokeLinejoin="round">
      <g transform="translate(12 12)" vectorEffect="non-scaling-stroke">
        <BidirectionalArrow />
        <g transform="rotate(120)"><BidirectionalArrow /></g>
        <g transform="rotate(240)"><BidirectionalArrow /></g>
      </g>
    </svg>
  )
}

/**
 * Sidebar / login badge: custom logo image when set, else BrandMark on gradient.
 * size: outer badge px (default 42 sidebar / 46 login).
 */
export function PanelBrandBadge({ logoUrl, size = 42, markClassName, title }) {
  const s = size
  const radius = Math.round(s * 14 / 42)
  if (logoUrl) {
    return (
      <div
        className="flex-none overflow-hidden ring-1 ring-black/5 dark:ring-white/15 shadow-[0_10px_24px_-8px_rgba(196,120,90,0.45)] bg-surface"
        style={{ width: s, height: s, borderRadius: radius }}
        title={title}
      >
        <img src={logoUrl} alt="" className="w-full h-full object-cover" draggable={false} />
      </div>
    )
  }
  return (
    <div
      className="flex-none grid place-items-center text-white shadow-[0_10px_24px_-8px_rgba(196,120,90,0.55)] ring-1 ring-white/30"
      style={{
        width: s,
        height: s,
        borderRadius: radius,
        background: 'linear-gradient(145deg, #d4896a 0%, #c4785a 55%, #b8664a 100%)',
      }}
      title={title}
    >
      <BrandMark className={markClassName || (s >= 46 ? 'w-[28px] h-[28px]' : 'w-[26px] h-[26px]')} />
    </div>
  )
}

// One side of the equilateral layout: horizontal double arrow, scaled and
// offset so the three copies form a clear triangle without shrinking into
// a tiny white knot in the badge center.
function BidirectionalArrow() {
  // radius ≈ 5.5 keeps tips near the badge edge; scale 0.72 enlarges glyphs.
  return (
    <g transform="translate(0 5.5) scale(0.72)">
      <g transform="translate(-12 -12)">
        <path d="M17 7 21 11 17 15" />
        <path d="M21 11H7" />
        <path d="M7 17 3 13 7 9" />
        <path d="M3 13H17" />
      </g>
    </g>
  )
}
