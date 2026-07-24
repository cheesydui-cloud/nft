import { useEffect, useState } from 'react'
import QRCode from 'qrcode'
import { Modal } from './ui'
import { copyToClipboard } from '../lib/clipboard'

/** Show a scannable QR for a proxy URI (or any text). */
export function QRCodeModal({ open, onClose, text, title = '扫码导入', toast }) {
  const [dataUrl, setDataUrl] = useState('')
  const [err, setErr] = useState('')

  useEffect(() => {
    if (!open || !text) {
      setDataUrl('')
      setErr('')
      return
    }
    let cancelled = false
    QRCode.toDataURL(text, {
      width: 280,
      margin: 2,
      errorCorrectionLevel: 'M',
      color: { dark: '#0f172a', light: '#ffffff' },
    }).then(url => {
      if (!cancelled) { setDataUrl(url); setErr('') }
    }).catch(e => {
      if (!cancelled) { setDataUrl(''); setErr(e?.message || '生成失败') }
    })
    return () => { cancelled = true }
  }, [open, text])

  const copy = async () => {
    if (!text) return
    try {
      await copyToClipboard(text)
      toast?.('已复制链接')
    } catch {
      toast?.('复制失败', 'error')
    }
  }

  return (
    <Modal open={open} onClose={onClose} title={title}>
      {!text ? (
        <p className="text-sm text-ink-soft">没有可生成二维码的链接。</p>
      ) : (
        <div className="flex flex-col items-center gap-4">
          {err && <p className="text-sm text-red-600">{err}</p>}
          {dataUrl ? (
            <img src={dataUrl} alt="二维码" className="w-[280px] h-[280px] rounded-lg border border-line bg-white" />
          ) : (
            !err && <div className="w-[280px] h-[280px] rounded-lg border border-line bg-raised animate-pulse" />
          )}
          <p className="text-[12px] text-ink-mut text-center max-w-[320px] break-all font-mono leading-relaxed">
            {text.length > 160 ? `${text.slice(0, 160)}…` : text}
          </p>
          <div className="flex gap-2 w-full justify-end">
            <button type="button" className="btn-secondary text-xs" onClick={copy}>复制链接</button>
            <button type="button" className="btn-primary text-xs" onClick={onClose}>关闭</button>
          </div>
        </div>
      )}
    </Modal>
  )
}

/** Compact button that opens QRCodeModal for the given text. */
export function QRCodeButton({ text, disabled, className = '', label = '二维码', title = '扫码导入', toast, onOpen }) {
  const [open, setOpen] = useState(false)
  const can = !!text && !disabled
  return (
    <>
      <button
        type="button"
        disabled={!can}
        title={can ? title : '暂无可导入链接'}
        onClick={() => {
          if (onOpen) onOpen()
          setOpen(true)
        }}
        className={className || 'text-emerald-600 text-xs font-semibold hover:underline disabled:opacity-40 disabled:cursor-not-allowed'}
      >
        {label}
      </button>
      <QRCodeModal open={open} onClose={() => setOpen(false)} text={text} title={title} toast={toast} />
    </>
  )
}
