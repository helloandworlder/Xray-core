package dispatcher

import (
	"sync"
	"time"

	"github.com/xtls/xray-core/app/userratelimit"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
)

type byteRateLimiter struct {
	rate  float64
	burst float64

	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func newByteRateLimiter(bps int64) *byteRateLimiter {
	if bps <= 0 {
		return nil
	}
	rate := float64(bps) / 8.0
	if rate <= 0 {
		return nil
	}
	return &byteRateLimiter{rate: rate, burst: rate, tokens: rate, last: time.Now()}
}

func (l *byteRateLimiter) waitBytes(n int) {
	if l == nil || l.rate <= 0 || n <= 0 {
		return
	}
	now := time.Now()
	l.mu.Lock()
	if l.last.IsZero() {
		l.last = now
		l.tokens = l.burst
	}
	if now.After(l.last) {
		l.tokens += now.Sub(l.last).Seconds() * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
	}
	required := float64(n)
	if l.tokens >= required {
		l.tokens -= required
		l.mu.Unlock()
		return
	}
	deficit := required - l.tokens
	wait := time.Duration(deficit / l.rate * float64(time.Second))
	if wait < 0 {
		wait = 0
	}
	l.tokens = 0
	l.last = now.Add(wait)
	l.mu.Unlock()
	if wait > 0 {
		time.Sleep(wait)
	}
}

type rateLimitWriter struct {
	writer  buf.Writer
	limiter *byteRateLimiter
}

func newRateLimitWriter(writer buf.Writer, bps int64) buf.Writer {
	limiter := newByteRateLimiter(bps)
	if limiter == nil || writer == nil {
		return writer
	}
	return &rateLimitWriter{writer: writer, limiter: limiter}
}

func (w *rateLimitWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	w.limiter.waitBytes(int(mb.Len()))
	return w.writer.WriteMultiBuffer(mb)
}

func (w *rateLimitWriter) Close() error {
	return common.Close(w.writer)
}

func (w *rateLimitWriter) Interrupt() {
	common.Interrupt(w.writer)
}

type rateLimitReader struct {
	reader  buf.Reader
	limiter *byteRateLimiter
}

func newRateLimitReader(reader buf.Reader, bps int64) buf.Reader {
	limiter := newByteRateLimiter(bps)
	if limiter == nil || reader == nil {
		return reader
	}
	if timeoutReader, ok := reader.(buf.TimeoutReader); ok {
		return &rateLimitTimeoutReader{timeoutReader: timeoutReader, limiter: limiter}
	}
	return &rateLimitReader{reader: reader, limiter: limiter}
}

func (r *rateLimitReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.reader.ReadMultiBuffer()
	r.limiter.waitBytes(int(mb.Len()))
	return mb, err
}

type rateLimitTimeoutReader struct {
	timeoutReader buf.TimeoutReader
	limiter       *byteRateLimiter
}

func (r *rateLimitTimeoutReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.timeoutReader.ReadMultiBuffer()
	r.limiter.waitBytes(int(mb.Len()))
	return mb, err
}

func (r *rateLimitTimeoutReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	mb, err := r.timeoutReader.ReadMultiBufferTimeout(timeout)
	r.limiter.waitBytes(int(mb.Len()))
	return mb, err
}

func resolveUserRateLimit(email string) (int64, int64) {
	item, ok := userratelimit.Get(email)
	if !ok {
		return 0, 0
	}
	return item.UplinkBps, item.DownlinkBps
}
