import React from 'react'

// Minimal WHEP client for MediaMTX. Creates a PeerConnection and attaches to a video element.
async function startWhep(video: HTMLVideoElement, whepUrl: string, onDisconnected?: () => void) {
  const pc = new RTCPeerConnection({ iceServers: [{ urls: ['stun:stun.l.google.com:19302'] }] })

  pc.addEventListener('track', (ev) => {
    if (ev.streams && ev.streams[0]) {
      video.srcObject = ev.streams[0]
    } else {
      const stream = new MediaStream()
      stream.addTrack(ev.track)
      video.srcObject = stream
    }
  })
  const stateHandler = () => {
    const st = pc.connectionState
    if (st === 'failed' || st === 'disconnected' || st === 'closed') onDisconnected?.()
  }
  pc.addEventListener('connectionstatechange', stateHandler)
  pc.addEventListener('iceconnectionstatechange', stateHandler)

  const offer = await pc.createOffer({ offerToReceiveVideo: true, offerToReceiveAudio: false })
  await pc.setLocalDescription(offer)
  const res = await fetch(whepUrl, { method: 'POST', headers: { 'Content-Type': 'application/sdp' }, body: offer.sdp ?? '' })
  if (!res.ok) throw new Error('WHEP start failed: ' + res.status)
  const answerSdp = await res.text()
  await pc.setRemoteDescription({ type: 'answer', sdp: answerSdp })

  video.muted = true
  video.playsInline = true
  video.autoplay = true

  return () => {
    try { pc.close() } catch {}
    if (video.srcObject instanceof MediaStream) {
      video.srcObject.getTracks().forEach((t) => t.stop())
    }
    video.srcObject = null
  }
}

// Props: provide rtspUrl and optional name. Component will POST /cameras/start and play via WHEP.
export default function ReactWhepSample({
  rtspUrl,
  name,
  gatewayBase = 'http://127.0.0.1:8090',
  whepBase = 'http://127.0.0.1:8889',
  style,
}: {
  rtspUrl: string
  name?: string
  gatewayBase?: string
  whepBase?: string
  style?: React.CSSProperties
}) {
  const videoRef = React.useRef<HTMLVideoElement | null>(null)
  const stopRef = React.useRef<null | (() => void)>(null)
  const [status, setStatus] = React.useState<'idle' | 'connecting' | 'live' | 'error'>('idle')
  const [error, setError] = React.useState<string | null>(null)

  const connect = React.useCallback(async () => {
    if (!rtspUrl) return
    setStatus('connecting')
    setError(null)
    let streamName = name || rtspUrl.replace('rtsp://', '').replace(/[/:]/g, '-').toLowerCase().slice(0, 48)
    try {
      const resp = await fetch(`${gatewayBase}/cameras/start`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: streamName, rtsp: rtspUrl, pub_fps: 20, scale_width: 640 }),
      })
      if (resp.ok) {
        const info = await resp.json().catch(() => null)
        if (info && info.name) streamName = info.name
      }
      if (!videoRef.current) return
      stopRef.current?.()
      const stop = await startWhep(videoRef.current, `${whepBase}/whep/${encodeURIComponent(streamName)}`, () => {
        // reconnect after delay
        setTimeout(() => connect(), 1500)
      })
      stopRef.current = stop
      setStatus('live')
    } catch (e: any) {
      setError(String(e))
      setStatus('error')
      setTimeout(() => connect(), 1500)
    }
  }, [gatewayBase, whepBase, name, rtspUrl])

  React.useEffect(() => {
    connect()
    return () => {
      try { stopRef.current?.() } catch {}
      stopRef.current = null
    }
  }, [connect])

  return (
    <div style={{ position: 'relative', ...style }}>
      <video ref={videoRef} style={{ width: '100%', height: '100%', background: '#000' }} />
      {status !== 'live' && (
        <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#fff', background: 'rgba(0,0,0,0.4)' }}>
          {status === 'connecting' ? 'Connecting…' : status === 'error' ? `Error: ${error}` : 'Idle'}
        </div>
      )}
    </div>
  )
}
