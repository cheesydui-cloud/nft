/* Lightweight Markdown → React for usage docs.
   Supports: headings, paragraphs, lists, code, blockquote, links, images, hr, bold/italic/code.
   No raw HTML. Unsafe schemes on links/images are blocked. */

function escapeHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function safeURL(url) {
  const u = String(url || '').trim()
  if (!u) return null
  // Allow same-origin asset paths and relative paths used by our upload API.
  if (u.startsWith('/api/docs/assets/') || u.startsWith('./') || u.startsWith('../') || u.startsWith('/')) {
    // Block protocol-relative and weird path tricks
    if (u.startsWith('//')) return null
    return u
  }
  try {
    const parsed = new URL(u, 'https://example.invalid')
    if (parsed.protocol === 'http:' || parsed.protocol === 'https:') return u
  } catch {}
  return null
}

function renderInline(text, keyPrefix = 'i') {
  const nodes = []
  // Order: code, image, link, bold, italic
  const re = /(`[^`]+`)|(!\[[^\]]*\]\([^)]+\))|(\[[^\]]+\]\([^)]+\))|(\*\*[^*]+\*\*)|(__[^_]+__)|(\*[^*]+\*)|(_[^_]+_)/g
  let last = 0
  let m
  let k = 0
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) {
      nodes.push(text.slice(last, m.index))
    }
    const token = m[0]
    if (token.startsWith('`')) {
      nodes.push(<code key={`${keyPrefix}-c${k++}`} className="md-code">{token.slice(1, -1)}</code>)
    } else if (token.startsWith('![')) {
      const im = token.match(/^!\[([^\]]*)\]\(([^)]+)\)$/)
      const src = im && safeURL(im[2])
      if (src) {
        nodes.push(
          <img key={`${keyPrefix}-img${k++}`} src={src} alt={im[1] || ''} className="md-img" loading="lazy" />
        )
      } else {
        nodes.push(token)
      }
    } else if (token.startsWith('[')) {
      const lm = token.match(/^\[([^\]]+)\]\(([^)]+)\)$/)
      const href = lm && safeURL(lm[2])
      if (href) {
        const external = /^https?:/i.test(href)
        nodes.push(
          <a key={`${keyPrefix}-a${k++}`} href={href} className="md-link"
            {...(external ? { target: '_blank', rel: 'noopener noreferrer' } : {})}>
            {lm[1]}
          </a>
        )
      } else {
        nodes.push(token)
      }
    } else if (token.startsWith('**') || token.startsWith('__')) {
      nodes.push(<strong key={`${keyPrefix}-b${k++}`}>{token.slice(2, -2)}</strong>)
    } else if (token.startsWith('*') || token.startsWith('_')) {
      nodes.push(<em key={`${keyPrefix}-e${k++}`}>{token.slice(1, -1)}</em>)
    } else {
      nodes.push(token)
    }
    last = m.index + token.length
  }
  if (last < text.length) nodes.push(text.slice(last))
  return nodes
}

function flushParagraph(buf, out, key) {
  if (!buf.length) return
  const text = buf.join('\n').trim()
  if (text) out.push(<p key={key} className="md-p">{renderInline(text, key)}</p>)
  buf.length = 0
}

/** Render a Markdown string into a React tree. */
export function Markdown({ source, className = '' }) {
  const text = String(source || '').replace(/\r\n/g, '\n')
  const lines = text.split('\n')
  const out = []
  const para = []
  let i = 0
  let key = 0

  while (i < lines.length) {
    const line = lines[i]
    const trimmed = line.trim()

    // fenced code
    if (trimmed.startsWith('```')) {
      flushParagraph(para, out, `p${key++}`)
      const lang = trimmed.slice(3).trim()
      i++
      const codeLines = []
      while (i < lines.length && !lines[i].trim().startsWith('```')) {
        codeLines.push(lines[i])
        i++
      }
      if (i < lines.length) i++ // closing fence
      out.push(
        <pre key={`code${key++}`} className="md-pre" data-lang={lang || undefined}>
          <code>{codeLines.join('\n')}</code>
        </pre>
      )
      continue
    }

    // blank line ends paragraph
    if (trimmed === '') {
      flushParagraph(para, out, `p${key++}`)
      i++
      continue
    }

    // hr
    if (/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) {
      flushParagraph(para, out, `p${key++}`)
      out.push(<hr key={`hr${key++}`} className="md-hr" />)
      i++
      continue
    }

    // heading
    const hm = trimmed.match(/^(#{1,6})\s+(.+)$/)
    if (hm) {
      flushParagraph(para, out, `p${key++}`)
      const level = hm[1].length
      const Tag = `h${level}`
      out.push(
        <Tag key={`h${key++}`} className={`md-h md-h${level}`}>
          {renderInline(hm[2], `h${key}`)}
        </Tag>
      )
      i++
      continue
    }

    // blockquote
    if (trimmed.startsWith('>')) {
      flushParagraph(para, out, `p${key++}`)
      const q = []
      while (i < lines.length && lines[i].trim().startsWith('>')) {
        q.push(lines[i].trim().replace(/^>\s?/, ''))
        i++
      }
      out.push(
        <blockquote key={`q${key++}`} className="md-quote">
          {q.map((ql, qi) => <p key={qi} className="md-p">{renderInline(ql, `q${key}-${qi}`)}</p>)}
        </blockquote>
      )
      continue
    }

    // unordered list
    if (/^[-*+]\s+/.test(trimmed)) {
      flushParagraph(para, out, `p${key++}`)
      const items = []
      while (i < lines.length && /^[-*+]\s+/.test(lines[i].trim())) {
        items.push(lines[i].trim().replace(/^[-*+]\s+/, ''))
        i++
      }
      out.push(
        <ul key={`ul${key++}`} className="md-ul">
          {items.map((it, ii) => <li key={ii}>{renderInline(it, `ul${key}-${ii}`)}</li>)}
        </ul>
      )
      continue
    }

    // ordered list
    if (/^\d+\.\s+/.test(trimmed)) {
      flushParagraph(para, out, `p${key++}`)
      const items = []
      while (i < lines.length && /^\d+\.\s+/.test(lines[i].trim())) {
        items.push(lines[i].trim().replace(/^\d+\.\s+/, ''))
        i++
      }
      out.push(
        <ol key={`ol${key++}`} className="md-ol">
          {items.map((it, ii) => <li key={ii}>{renderInline(it, `ol${key}-${ii}`)}</li>)}
        </ol>
      )
      continue
    }

    // standalone image line → block figure
    const onlyImg = trimmed.match(/^!\[([^\]]*)\]\(([^)]+)\)$/)
    if (onlyImg) {
      flushParagraph(para, out, `p${key++}`)
      const src = safeURL(onlyImg[2])
      if (src) {
        out.push(
          <figure key={`fig${key++}`} className="md-figure">
            <img src={src} alt={onlyImg[1] || ''} className="md-img" loading="lazy" />
            {onlyImg[1] ? <figcaption className="md-cap">{onlyImg[1]}</figcaption> : null}
          </figure>
        )
      } else {
        para.push(line)
      }
      i++
      continue
    }

    para.push(line)
    i++
  }
  flushParagraph(para, out, `p${key++}`)

  if (!out.length) {
    return <div className={`md-body ${className}`}><p className="md-p text-ink-mut">（空文档）</p></div>
  }
  return <div className={`md-body ${className}`}>{out}</div>
}

// Keep escape helper available for future sanitizers.
export { escapeHtml }
