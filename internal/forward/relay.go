package forward

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"nft/internal/nft"
)

// relayBufSize is the copy buffer per direction. Wrapping the source reader
// (below) makes io.CopyBuffer take the generic buffered path instead of
// splice — required so byte accounting updates continuously over the life of
// a connection, not only at close. This mirrors realm's behavior when its
// traffic counter is enabled; we always count (for quota), so we always copy.
const relayBufSize = 64 * 1024

// relayBufPool recycles the per-direction 64KB copy buffers. At 1024 conns ×
// 2 directions that's ~128MB of churn per port otherwise; pooling keeps it
// bounded and cuts GC pressure under connection turnover.
var relayBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, relayBufSize)
		return &b
	},
}

const dialTimeout = 10 * time.Second

// keepAlivePeriod paces TCP keepalive probes on both the client and upstream
// legs so a peer that dies without sending FIN/RST is detected by the kernel
// and the relay goroutine unblocks instead of leaking.
const keepAlivePeriod = 30 * time.Second

// relayLinger bounds how long the surviving direction may run after the other
// direction has half-closed. A peer stuck in FIN_WAIT2 is not probed by
// keepalive (the kernel only keepalives ESTABLISHED sockets), so without this
// the goroutine — and its per-port semaphore slot — would pin forever. It is a
// var, not a const, so tests can shrink it. The window is generous because a
// half-close legitimately precedes a long tail of response data.
var relayLinger = 60 * time.Second

// relayIdleTimeout force-closes a connection that has neither sent nor received
// any bytes for this long. TCP keepalive only catches peers that vanish; a peer
// that keeps the socket ESTABLISHED but sends nothing (deliberately, to exhaust
// the per-port semaphore) is invisible to keepalive. Rolling a read deadline
// forward on every Read reclaims those idle slots. It is a var so tests can
// shrink it. The window is long enough not to disturb legitimately idle
// long-lived sessions (e.g. an interactive shell left open).
var relayIdleTimeout = 10 * time.Minute

type target struct{ addr string }

func targetAddr(r nft.Rule) string {
	return net.JoinHostPort(r.DestIP, strconv.Itoa(r.DestPort))
}

// setKeepAlive enables TCP keepalive on c when it is a TCP connection; it is a
// no-op for any other conn type (e.g. test pipes).
func setKeepAlive(c net.Conn) {
	if tcp, ok := c.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(keepAlivePeriod)
	}
}

// dialUpstream is the single entry point for opening an upstream leg, so every
// pooled or on-demand connection gets the same dial timeout and keepalive.
func dialUpstream(addr string) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, err
	}
	setKeepAlive(c)
	return c, nil
}

// makeLimiter converts a Mbps cap into a byte/sec token-bucket limiter, or nil
// when unlimited. Burst must be >= the largest single WaitN call (one buffer).
func makeLimiter(mbps int) *rate.Limiter {
	if mbps <= 0 {
		return nil
	}
	bytesPerSec := float64(mbps) * 1e6 / 8.0
	burst := int(bytesPerSec)
	if burst < relayBufSize {
		burst = relayBufSize
	}
	return rate.NewLimiter(rate.Limit(bytesPerSec), burst)
}

// groupBurst sizes a shared bucket's burst: one second of quota, floored at
// the copy buffer so a single WaitN can never exceed the burst.
func groupBurst(bytesPerSec float64) int {
	burst := int(bytesPerSec)
	if burst < relayBufSize {
		burst = relayBufSize
	}
	return burst
}

// meteredReader rate-limits and/or counts each Read, and rolls an idle read
// deadline forward so a silent-but-alive peer can't pin the connection. limPtr
// may hold nil (unlimited); counter may be nil (don't count this direction);
// idle is the source conn used for the deadline, or nil to disable it.
type meteredReader struct {
	src     io.Reader
	idle    net.Conn
	limPtr  *atomic.Pointer[rate.Limiter]
	counter *atomic.Int64
	ctx     context.Context
}

func (r *meteredReader) Read(p []byte) (int, error) {
	if r.idle != nil {
		_ = r.idle.SetReadDeadline(time.Now().Add(relayIdleTimeout))
	}
	n, err := r.src.Read(p)
	if n > 0 {
		if r.limPtr != nil {
			if lim := r.limPtr.Load(); lim != nil {
				if werr := lim.WaitN(r.ctx, n); werr != nil {
					return n, werr
				}
			}
		}
		if r.counter != nil {
			r.counter.Add(int64(n))
		}
	}
	return n, err
}

// relayCopy copies src->dst, pacing/counting each chunk and enforcing an idle
// read deadline when src is a net.Conn.
func relayCopy(ctx context.Context, dst io.Writer, src io.Reader, limPtr *atomic.Pointer[rate.Limiter], counter *atomic.Int64) {
	mr := &meteredReader{src: src, limPtr: limPtr, counter: counter, ctx: ctx}
	if c, ok := src.(net.Conn); ok {
		mr.idle = c
	}
	bufp := relayBufPool.Get().(*[]byte)
	defer relayBufPool.Put(bufp)
	_, _ = io.CopyBuffer(dst, mr, *bufp)
}

// halfCloseWrite propagates a one-directional EOF so protocols that signal end
// of stream by closing one half keep working.
func halfCloseWrite(c net.Conn) {
	if tcp, ok := c.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}
