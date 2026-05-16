package dispatcher

import (
	"context"
	"sync"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
)

var errUserConnectionLimit = errors.New("user connection limit reached")

type userConnectionRegistry struct {
	mu     sync.Mutex
	active map[string]int32
}

func (r *userConnectionRegistry) acquire(key string, limit int32) (func(), bool) {
	if key == "" || limit <= 0 {
		return func() {}, true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		r.active = make(map[string]int32)
	}
	if r.active[key] >= limit {
		return nil, false
	}
	r.active[key]++
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.active[key]--
		if r.active[key] <= 0 {
			delete(r.active, key)
		}
	}, true
}

type rateLimiter struct {
	rate   int64
	mu     sync.Mutex
	tokens int64
	last   time.Time
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
}

func newRateLimiter(rate int64) *rateLimiter {
	return &rateLimiter{
		rate:  rate,
		now:   time.Now,
		sleep: sleepContext,
	}
}

func (l *rateLimiter) wait(ctx context.Context, n int64) error {
	if l == nil || l.rate <= 0 || n <= 0 {
		return nil
	}

	for {
		l.mu.Lock()
		now := l.now()
		burst := l.rate
		if n > burst {
			burst = n
		}
		if l.last.IsZero() {
			l.last = now
			l.tokens = burst
		}
		elapsed := now.Sub(l.last)
		if elapsed > 0 {
			l.tokens += int64(elapsed.Seconds() * float64(l.rate))
			if l.tokens > burst {
				l.tokens = burst
			}
			l.last = now
		}
		if l.tokens >= n {
			l.tokens -= n
			l.mu.Unlock()
			return nil
		}
		missing := n - l.tokens
		wait := time.Duration(float64(missing) / float64(l.rate) * float64(time.Second))
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		l.mu.Unlock()
		if err := l.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type rateLimitedReader struct {
	ctx     context.Context
	limiter *rateLimiter
	Reader  buf.Reader
}

func (r *rateLimitedReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.Reader.ReadMultiBuffer()
	if !mb.IsEmpty() {
		if waitErr := r.limiter.wait(r.ctx, int64(mb.Len())); waitErr != nil {
			buf.ReleaseMulti(mb)
			return nil, waitErr
		}
	}
	return mb, err
}

type rateLimitedTimeoutReader struct {
	ctx     context.Context
	limiter *rateLimiter
	Reader  buf.TimeoutReader
}

func (r *rateLimitedTimeoutReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.Reader.ReadMultiBuffer()
	if !mb.IsEmpty() {
		if waitErr := r.limiter.wait(r.ctx, int64(mb.Len())); waitErr != nil {
			buf.ReleaseMulti(mb)
			return nil, waitErr
		}
	}
	return mb, err
}

func (r *rateLimitedTimeoutReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	mb, err := r.Reader.ReadMultiBufferTimeout(timeout)
	if !mb.IsEmpty() {
		if waitErr := r.limiter.wait(r.ctx, int64(mb.Len())); waitErr != nil {
			buf.ReleaseMulti(mb)
			return nil, waitErr
		}
	}
	return mb, err
}

type rateLimitedWriter struct {
	ctx     context.Context
	limiter *rateLimiter
	Writer  buf.Writer
}

func (w *rateLimitedWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	if !mb.IsEmpty() {
		if err := w.limiter.wait(w.ctx, int64(mb.Len())); err != nil {
			buf.ReleaseMulti(mb)
			return err
		}
	}
	return w.Writer.WriteMultiBuffer(mb)
}

func userPolicyKey(ctx context.Context, user *protocol.MemoryUser) string {
	if user == nil || user.Email == "" {
		return ""
	}
	inbound := session.InboundFromContext(ctx)
	if inbound == nil || inbound.Tag == "" {
		return user.Email
	}
	return inbound.Tag + "\x00" + user.Email
}
