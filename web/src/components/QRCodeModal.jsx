import { useEffect, useState } from 'react'
import QRCode from 'qrcode'
import { Modal } from './ui'
import { copyToClipboard } from '../lib/clipboard'

const QR_DISPLAY = 320
const QR_PIXELS = 640

function qrPayload(text) {
  const raw = String(text || '').replace(/^\uFEFF/, '').trim()
  if (!raw) return ''
  // One scannable symbol only. Clipboard may join dual-stack with \n;
  // in-app scanners reject that as a single import.
  const line = raw.split(/\r?\n/).map(s => s.trim()).find(Boolean)
  return line || ''
}

/** Show a scannable QR for a proxy URI (or any text). */
export function QRCodeModal({ open, onClose, text, title = '扫码导入', toast }) {
  const [dataUrl, setDataUrl] = useState('')
  const [err, setErr] = useState('')
  const payload = qrPayload(text)

  useEffect(() => {
    if (!open || !payload) {
      setDataUrl('')
      setErr('')
      return
    }
    let cancelled = false
    // L = larger modules / more capacity. Render 2× display size so Retina
    // screens don't upsample a 280px bitmap and smear the modules.
    QRCode.toDataURL(payload, {
      width: QR_PIXELS,
      margin: 4,
      errorCorrectionLevel: 'L',
      color: { dark: '#000000', light: '#ffffff' },
    }).then(url => {
      if (!cancelled) { setDataUrl(url); setErr('') }
    }).catch(e => {
      if (!cancelled) { setDataUrl(''); setErr(e?.message || '生成失败') }
    })
    return () => { cancelled = true }
  }, [open, payload])

  const copy = async () => {
    if (!payload) return
    try {
      await copyToClipboard(payload)
      toast?.('已复制链接')
    } catch {
      toast?.('复制失败', 'error')
    }
  }

  return (
    <Modal open={open} onClose={onClose} title={title}>
      {!payload ? (
        <p className="text-sm text-ink-soft">没有可生成二维码的链接。</p>
      ) : (
        <div className="flex flex-col items-center gap-4">
          {err && <p className="text-sm text-red-600">{err}</p>}
          {dataUrl ? (
            <img
              src={dataUrl}
              alt="二维码"
              width={QR_DISPLAY}
              height={QR_DISPLAY}
              className="rounded-lg border border-line bg-white p-1"
              style={{
                width: QR_DISPLAY,
                height: QR_DISPLAY,
                imageRendering: 'pixelated',
              }}
            />
          ) : (
            !err && <div className="rounded-lg border border-line bg-raised animate-pulse" style={{ width: QR_DISPLAY, height: QR_DISPLAY }} />
          )}
          <p className="text-[11px] text-ink-mut text-center max-w-[320px]">请用客户端内「扫码」对准屏幕；系统相机通常无法导入代理链接。</p>
          <p className="text-[12px] text-ink-mut text-center max-w-[320px] break-all font-mono leading-relaxed">
            {payload.length > 160 ? `${payload.slice(0, 160)}…` : payload}
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
  const can = !!qrPayload(text) && !disabled
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
