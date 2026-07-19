// Triangle of three bidirectional arrows — the panel brand mark (scheme A).
export function BrandMark({ className = 'w-[22px] h-[22px]' }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <g transform="translate(12 12)" vectorEffect="non-scaling-stroke">
        <BidirectionalArrow />
        <g transform="rotate(120)"><BidirectionalArrow /></g>
        <g transform="rotate(240)"><BidirectionalArrow /></g>
      </g>
    </svg>
  )
}

// One side of the triangle: the original horizontal bidirectional glyph, scaled and
// pushed outward so the three copies meet at the vertices.
function BidirectionalArrow() {
  return (
    <g transform="translate(0 4.4) scale(0.52)">
      <g transform="translate(-12 -12)">
        <path d="M17 7 21 11 17 15" />
        <path d="M21 11H7" />
        <path d="M7 17 3 13 7 9" />
        <path d="M3 13H17" />
      </g>
    </g>
  )
}
