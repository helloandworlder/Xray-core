package rayipruntime

import (
	"context"
	"sync"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/transport"
)

const runtimeThrottleSleep = 20 * time.Millisecond

type contextKey string

const wrappedContextKey contextKey = "rayip-runtime-wrapped"

func ContextWithWrappedLink(ctx context.Context) context.Context {
	return context.WithValue(ctx, wrappedContextKey, true)
}

func HasWrappedLink(ctx context.Context) bool {
	wrapped, _ := ctx.Value(wrappedContextKey).(bool)
	return wrapped
}

func WrapLink(email string, manager *Manager, link *transport.Link, readerDirection Direction, writerDirection Direction) (*transport.Link, func(), error) {
	if email == "" || manager == nil || link == nil {
		return link, func() {}, nil
	}
	release, err := manager.AcquireConnection(email)
	if err != nil {
		return nil, nil, err
	}
	var once sync.Once
	releaseOnce := func() { once.Do(release) }
	return &transport.Link{
		Reader: &runtimeReader{
			email:     email,
			direction: readerDirection,
			manager:   manager,
			reader:    link.Reader,
			release:   releaseOnce,
		},
		Writer: &runtimeWriter{
			email:     email,
			direction: writerDirection,
			manager:   manager,
			writer:    link.Writer,
			release:   releaseOnce,
		},
	}, releaseOnce, nil
}

type runtimeReader struct {
	email     string
	direction Direction
	manager   *Manager
	reader    buf.Reader
	pending   buf.MultiBuffer
	release   func()
}

func (r *runtimeReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	return r.readMultiBufferWithDeadline(time.Time{})
}

func (r *runtimeReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	if timeout <= 0 {
		return r.ReadMultiBuffer()
	}
	return r.readMultiBufferWithDeadline(time.Now().Add(timeout))
}

func (r *runtimeReader) Interrupt() {
	r.pending = buf.ReleaseMulti(r.pending)
	if r.release != nil {
		r.release()
	}
	common.Interrupt(r.reader)
}

func (r *runtimeReader) readMultiBufferWithDeadline(deadline time.Time) (buf.MultiBuffer, error) {
	for {
		if !r.pending.IsEmpty() {
			if mb, ok := r.takePending(deadline); ok {
				return mb, nil
			}
			return nil, nil
		}

		var mb buf.MultiBuffer
		var err error
		if timeoutReader, ok := r.reader.(buf.TimeoutReader); ok && !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return nil, nil
			}
			mb, err = timeoutReader.ReadMultiBufferTimeout(remaining)
		} else {
			mb, err = r.reader.ReadMultiBuffer()
		}
		if mb.IsEmpty() {
			if err != nil && r.release != nil {
				r.release()
			}
			return mb, err
		}
		r.pending = mb
		if err != nil {
			err = nil
		}
	}
}

func (r *runtimeReader) takePending(deadline time.Time) (buf.MultiBuffer, bool) {
	for !r.pending.IsEmpty() {
		allowed := r.manager.AllowBytes(r.email, r.direction, int64(r.pending.Len()))
		if allowed > 0 {
			if allowed > int64(r.pending.Len()) {
				allowed = int64(r.pending.Len())
			}
			var chunk buf.MultiBuffer
			r.pending, chunk = buf.SplitSize(r.pending, int32(allowed))
			r.manager.RecordTraffic(r.email, r.direction, int64(chunk.Len()))
			return chunk, true
		}
		if !sleepForBudget(deadline) {
			return nil, false
		}
	}
	return nil, false
}

type runtimeWriter struct {
	email     string
	direction Direction
	manager   *Manager
	writer    buf.Writer
	release   func()
}

func (w *runtimeWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	for !mb.IsEmpty() {
		allowed := w.manager.AllowBytes(w.email, w.direction, int64(mb.Len()))
		if allowed <= 0 {
			sleepForBudget(time.Time{})
			continue
		}
		if allowed > int64(mb.Len()) {
			allowed = int64(mb.Len())
		}
		var chunk buf.MultiBuffer
		mb, chunk = buf.SplitSize(mb, int32(allowed))
		size := int64(chunk.Len())
		if err := w.writer.WriteMultiBuffer(chunk); err != nil {
			buf.ReleaseMulti(mb)
			if w.release != nil {
				w.release()
			}
			return err
		}
		w.manager.RecordTraffic(w.email, w.direction, size)
	}
	return nil
}

func (w *runtimeWriter) Close() error {
	if w.release != nil {
		w.release()
	}
	return common.Close(w.writer)
}

func (w *runtimeWriter) Interrupt() {
	if w.release != nil {
		w.release()
	}
	common.Interrupt(w.writer)
}

func sleepForBudget(deadline time.Time) bool {
	sleep := runtimeThrottleSleep
	if !deadline.IsZero() {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		if remaining < sleep {
			sleep = remaining
		}
	}
	time.Sleep(sleep)
	return true
}
