package dispatcher

import (
	"sync"
	"time"

	"github.com/xtls/xray-core/app/rayipruntime"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/transport"
)

const rayIPRuntimeThrottleSleep = 20 * time.Millisecond

func wrapRayIPRuntimeLink(email string, manager *rayipruntime.Manager, link *transport.Link) (*transport.Link, func(), error) {
	return wrapRayIPRuntimeLinkWithDirections(email, manager, link, rayipruntime.DirectionEgress, rayipruntime.DirectionIngress)
}

func wrapRayIPRuntimeInboundLink(email string, manager *rayipruntime.Manager, link *transport.Link) (*transport.Link, func(), error) {
	return wrapRayIPRuntimeLinkWithDirections(email, manager, link, rayipruntime.DirectionIngress, rayipruntime.DirectionEgress)
}

func wrapRayIPRuntimeLinkWithDirections(email string, manager *rayipruntime.Manager, link *transport.Link, readerDirection rayipruntime.Direction, writerDirection rayipruntime.Direction) (*transport.Link, func(), error) {
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
		Reader: &rayIPRuntimeReader{
			email:     email,
			direction: readerDirection,
			manager:   manager,
			reader:    link.Reader,
			release:   releaseOnce,
		},
		Writer: &rayIPRuntimeWriter{
			email:     email,
			direction: writerDirection,
			manager:   manager,
			writer:    link.Writer,
			release:   releaseOnce,
		},
	}, releaseOnce, nil
}

type rayIPRuntimeReader struct {
	email     string
	direction rayipruntime.Direction
	manager   *rayipruntime.Manager
	reader    buf.Reader
	pending   buf.MultiBuffer
	release   func()
}

func (r *rayIPRuntimeReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	return r.readMultiBufferWithDeadline(time.Time{})
}

func (r *rayIPRuntimeReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	if timeout <= 0 {
		return r.ReadMultiBuffer()
	}
	return r.readMultiBufferWithDeadline(time.Now().Add(timeout))
}

func (r *rayIPRuntimeReader) Interrupt() {
	r.pending = buf.ReleaseMulti(r.pending)
	if r.release != nil {
		r.release()
	}
	common.Interrupt(r.reader)
}

func (r *rayIPRuntimeReader) readMultiBufferWithDeadline(deadline time.Time) (buf.MultiBuffer, error) {
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
			// Return the data first. The next read will surface the read-side close.
			err = nil
		}
	}
}

func (r *rayIPRuntimeReader) takePending(deadline time.Time) (buf.MultiBuffer, bool) {
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
		if !sleepForRuntimeBudget(deadline) {
			return nil, false
		}
	}
	return nil, false
}

type rayIPRuntimeWriter struct {
	email     string
	direction rayipruntime.Direction
	manager   *rayipruntime.Manager
	writer    buf.Writer
	release   func()
}

func (w *rayIPRuntimeWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	for !mb.IsEmpty() {
		allowed := w.manager.AllowBytes(w.email, w.direction, int64(mb.Len()))
		if allowed <= 0 {
			sleepForRuntimeBudget(time.Time{})
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

func (w *rayIPRuntimeWriter) Close() error {
	if w.release != nil {
		w.release()
	}
	return common.Close(w.writer)
}

func (w *rayIPRuntimeWriter) Interrupt() {
	if w.release != nil {
		w.release()
	}
	common.Interrupt(w.writer)
}

func sleepForRuntimeBudget(deadline time.Time) bool {
	sleep := rayIPRuntimeThrottleSleep
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
