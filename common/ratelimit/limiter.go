// Package ratelimit provides multi-level rate limiting for Xray-core.
package ratelimit

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"golang.org/x/time/rate"
)

// Limiter holds uplink and downlink rate limiters.
type Limiter struct {
	Uplink   *rate.Limiter
	Downlink *rate.Limiter
}

// NewLimiter creates a new Limiter with specified rates in bytes/sec.
// A rate of 0 or negative means unlimited.
func NewLimiter(uplinkBytesPerSec, downlinkBytesPerSec int64) *Limiter {
	l := &Limiter{}
	if uplinkBytesPerSec > 0 {
		// Burst size: allow 1 second worth of data, but cap at 512KB for smoother rate limiting
		burst := int(uplinkBytesPerSec)
		if burst > 512*1024 {
			burst = 512 * 1024
		}
		l.Uplink = rate.NewLimiter(rate.Limit(uplinkBytesPerSec), burst)
	}
	if downlinkBytesPerSec > 0 {
		burst := int(downlinkBytesPerSec)
		if burst > 512*1024 {
			burst = 512 * 1024
		}
		l.Downlink = rate.NewLimiter(rate.Limit(downlinkBytesPerSec), burst)
	}
	return l
}

// RateLimitedWriter wraps a buf.Writer with rate limiting.
type RateLimitedWriter struct {
	buf.Writer
	limiters []*rate.Limiter
	ctx      context.Context
}

// NewRateLimitedWriter creates a writer with multiple rate limiters.
func NewRateLimitedWriter(writer buf.Writer, ctx context.Context, limiters ...*rate.Limiter) *RateLimitedWriter {
	filtered := make([]*rate.Limiter, 0, len(limiters))
	for _, l := range limiters {
		if l != nil {
			filtered = append(filtered, l)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return &RateLimitedWriter{
		Writer:   writer,
		limiters: filtered,
		ctx:      ctx,
	}
}

// WriteMultiBuffer implements buf.Writer with rate limiting.
func (w *RateLimitedWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	size := int(mb.Len())
	if size > 0 {
		for _, limiter := range w.limiters {
			if err := limiter.WaitN(w.ctx, size); err != nil {
				buf.ReleaseMulti(mb)
				return err
			}
		}
	}
	return w.Writer.WriteMultiBuffer(mb)
}

// Close implements common.Closable.
func (w *RateLimitedWriter) Close() error {
	return common.Close(w.Writer)
}

// Interrupt implements common.Interruptible.
func (w *RateLimitedWriter) Interrupt() {
	common.Interrupt(w.Writer)
}

// RateLimitedReader wraps a buf.Reader with rate limiting.
type RateLimitedReader struct {
	buf.Reader
	limiters []*rate.Limiter
	ctx      context.Context
}

// NewRateLimitedReader creates a reader with multiple rate limiters.
func NewRateLimitedReader(reader buf.Reader, ctx context.Context, limiters ...*rate.Limiter) *RateLimitedReader {
	filtered := make([]*rate.Limiter, 0, len(limiters))
	for _, l := range limiters {
		if l != nil {
			filtered = append(filtered, l)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return &RateLimitedReader{
		Reader:   reader,
		limiters: filtered,
		ctx:      ctx,
	}
}

// ReadMultiBuffer implements buf.Reader with rate limiting.
func (r *RateLimitedReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.Reader.ReadMultiBuffer()
	if err != nil {
		return mb, err
	}
	size := int(mb.Len())
	if size > 0 {
		for _, limiter := range r.limiters {
			if err := limiter.WaitN(r.ctx, size); err != nil {
				buf.ReleaseMulti(mb)
				return nil, err
			}
		}
	}
	return mb, nil
}

var ratePattern = regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*(bps|kbps|mbps|gbps|b/s|kb/s|mb/s|gb/s)?$`)

// ParseRate parses a rate string like "10mbps", "1gbps", "1024kbps" to bytes/sec.
// Returns 0 for empty string or "0" (unlimited).
func ParseRate(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0
	}

	matches := ratePattern.FindStringSubmatch(s)
	if matches == nil {
		return 0
	}

	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0
	}

	unit := strings.ToLower(matches[2])
	var multiplier float64 = 1

	switch unit {
	case "bps", "b/s", "":
		multiplier = 1.0 / 8.0
	case "kbps", "kb/s":
		multiplier = 1024.0 / 8.0
	case "mbps", "mb/s":
		multiplier = 1024.0 * 1024.0 / 8.0
	case "gbps", "gb/s":
		multiplier = 1024.0 * 1024.0 * 1024.0 / 8.0
	}

	return int64(value * multiplier)
}
