import { useState } from 'react'

/* Shared list-page shell: rounded panel, page header, toolbar with search.
   One look across admin and user pages so the two never drift apart again.
   Colors use semantic tokens (surface/ink/line) so dark mode comes for free. */

/* Page title + item count, sits above the panel. */
export function PageHeader({ title, count, unit = '条' }) {
  return (
    <div className="flex items-baseline gap-3.5 mb-[22px]">
      <h1 className="m-0 text-2xl font-bold text-ink">{title}</h1>
      {count != null && <span className="text-[14px] text-ink-mut">共 {count} {unit}</span>}
    </div>
  )
}

/* Rounded card that wraps a list's toolbar and table. With `fill`, it grows to
   fill a flex-column page and lays its children out as a column so a
   TableScroll child can scroll while the toolbar stays put. */
export function Panel({ children, className = '', fill = false }) {
  return (
    <section className={`bg-surface border border-line rounded-[14px] shadow-[0_1px_2px_rgba(16,24,40,0.04)] overflow-hidden ${fill ? 'flex-1 min-h-0 flex flex-col' : ''} ${className}`}>
      {children}
    </section>
  )
}

/* Scroll container for a list table inside a `fill` Panel: only the rows
   scroll, while the sticky table header (and the Panel toolbar above) stay
   fixed. tbl-scroll adds horizontal scrolling with a sticky first column on
   mobile, for pages that render a real table there instead of cards. */
export function TableScroll({ children }) {
  return <div className="table-scroll tbl-scroll flex-1 min-h-0 overflow-auto">{children}</div>
}

/* Bounded scroll box for tables outside a `fill` Panel (dashboard cards,
   detail-page sections, modals): grows with its rows up to max-height, then
   the rows scroll locally under the sticky header instead of stretching the
   whole page. */
export function TableBox({ className = '', children }) {
  return <div className={`table-scroll tbl-scroll overflow-auto max-h-[460px] ${className}`}>{children}</div>
}

/* Toolbar row inside a Panel — typically a SearchInput plus a primary action. */
export function PanelToolbar({ children }) {
  return (
    <div className="flex items-center gap-4 px-[22px] py-[18px] border-b border-line-soft flex-wrap">
      {children}
    </div>
  )
}

/* Search box with a leading magnifier; controlled via value/onChange. */
export function SearchInput({ value, onChange, placeholder }) {
  return (
    <div className="relative flex-1 min-w-0 md:min-w-[240px] md:max-w-[340px]">
      <svg className="w-4 h-4 absolute left-[13px] top-1/2 -translate-y-1/2 text-ink-mut pointer-events-none" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" /></svg>
      <input value={value} onChange={e => onChange(e.target.value)} placeholder={placeholder}
        className="w-full text-[13.5px] pl-[38px] pr-3.5 py-[10px] bg-surface border border-line rounded-[9px] outline-none text-ink focus:border-blue-600 focus:ring-3 focus:ring-blue-600/10 transition-colors" />
    </div>
  )
}

/* ---------- DetailHeader: 详情页顶部统一标题区 ---------- */
export function DetailHeader({ title, badge, meta, actions, backTo, backLabel }) {
  return (
    <div className="mb-[22px]">
      {backTo && (
        <a href={backTo} className="inline-flex items-center gap-1 text-blue-600 text-[13px] font-semibold hover:underline mb-3">
          <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"><path d="M19 12H5M12 19l-7-7 7-7"/></svg>
          {backLabel || '返回列表'}
        </a>
      )}
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div className="min-w-0">
          <div className="flex items-center gap-3 flex-wrap">
            <h1 className="m-0 text-2xl font-bold text-ink">{title}</h1>
            {badge}
          </div>
          {meta && <p className="mt-1.5 text-[13px] text-ink-soft">{meta}</p>}
        </div>
        {actions && <div className="flex items-center gap-2 flex-wrap shrink-0">{actions}</div>}
      </div>
    </div>
  )
}

/* ---------- SectionCard: 详情页统一 section 容器 ---------- */
export function SectionCard({ title, subtitle, actions, children, className = '', collapsible = false, defaultOpen = true }) {
  const [open, setOpen] = useState(collapsible ? defaultOpen : true)
  return (
    <div className={`card mb-5 ${className}`}>
      <div className="card-header justify-between">
        <div className="flex items-center gap-2 min-w-0">
          {collapsible && (
            <button onClick={() => setOpen(o => !o)}
              className="text-ink-mut hover:text-ink p-0.5 -ml-0.5 transition-colors"
              aria-label={open ? '收起' : '展开'}>
              <svg className={`w-4 h-4 transition-transform ${open ? '' : '-rotate-90'}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m6 9 6 6 6-6"/></svg>
            </button>
          )}
          <h3 className="text-[15px] font-bold">{title}</h3>
          {subtitle && <span className="text-[13px] text-ink-mut">{subtitle}</span>}
        </div>
        {actions && <div className="flex items-center gap-2 flex-shrink-0">{actions}</div>}
      </div>
      {open && children}
    </div>
  )
}

/* ---------- InfoGrid: 键值对信息展示区 ---------- */
export function InfoGrid({ items, className = '', labelWidth = '120px' }) {
  return (
    <div className={`grid gap-x-4 gap-y-3 items-center text-[13.5px] ${className}`}
      style={{ gridTemplateColumns: `${labelWidth} 1fr` }}>
      {items.map((it, i) => (
        <InfoRow key={i} label={it.label} accent={it.accent} mono={it.mono}>{it.value}</InfoRow>
      ))}
    </div>
  )
}

function InfoRow({ label, accent, mono, children }) {
  return (
    <>
      <span className="fl">{label}</span>
      <span className={`${mono ? 'font-mono' : ''} ${accent ? 'font-semibold' : ''}`}>{children}</span>
    </>
  )
}

/* Primary toolbar action; right-aligned by default. */
export function ToolbarButton({ onClick, children, className = '', secondary }) {
  const base = secondary
    ? 'text-ink-soft bg-surface border border-line hover:bg-raised hover:text-ink'
    : 'text-white bg-blue-600 hover:bg-blue-700 border-0'
  return (
    <button onClick={onClick}
      className={`ml-auto inline-flex items-center gap-1.5 text-[13.5px] font-semibold px-4 py-[10px] rounded-[9px] cursor-pointer transition-colors ${base} ${className}`}>
      {children}
    </button>
  )
}
