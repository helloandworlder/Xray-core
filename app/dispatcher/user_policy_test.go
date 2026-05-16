package dispatcher

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
)

func TestUserConnectionRegistryRejectsOverLimit(t *testing.T) {
	var registry userConnectionRegistry

	release, ok := registry.acquire("inbound\x00user", 1)
	if !ok {
		t.Fatal("first connection should be accepted")
	}
	defer release()

	if _, ok := registry.acquire("inbound\x00user", 1); ok {
		t.Fatal("second connection should be rejected while first is active")
	}

	release()
	if release2, ok := registry.acquire("inbound\x00user", 1); !ok {
		t.Fatal("connection should be accepted after release")
	} else {
		release2()
	}
}

func TestUserPolicyKeyIncludesInboundTag(t *testing.T) {
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{
		Tag: "inbound-48084",
	})
	key := userPolicyKey(ctx, &protocol.MemoryUser{Email: "D-1738585248"})
	if key != "inbound-48084\x00D-1738585248" {
		t.Fatalf("unexpected key: %q", key)
	}
}

func TestUserConnectionRegistryAllowsSameEmailOnDifferentInbounds(t *testing.T) {
	var registry userConnectionRegistry

	releaseA, ok := registry.acquire("inbound-a\x00same@example", 1)
	if !ok {
		t.Fatal("first inbound should acquire connection")
	}
	defer releaseA()

	releaseB, ok := registry.acquire("inbound-b\x00same@example", 1)
	if !ok {
		t.Fatal("different inbound should not share the same email limit bucket")
	}
	releaseB()
}

func TestRateLimiterWaitsInsteadOfRejecting(t *testing.T) {
	now := time.Unix(0, 0)
	waited := time.Duration(0)
	limiter := newRateLimiter(100)
	limiter.now = func() time.Time {
		return now.Add(waited)
	}
	limiter.sleep = func(ctx context.Context, d time.Duration) error {
		waited += d
		return nil
	}

	if err := limiter.wait(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	if err := limiter.wait(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if waited <= 0 {
		t.Fatal("limiter should wait when payload exceeds available tokens")
	}
}

type oneShotReader struct {
	mb   buf.MultiBuffer
	read bool
}

func (r *oneShotReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	if r.read {
		return nil, io.EOF
	}
	r.read = true
	return r.mb, nil
}

type captureWriter struct {
	writes int
	bytes  int32
}

func (w *captureWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	w.writes++
	w.bytes += mb.Len()
	buf.ReleaseMulti(mb)
	return nil
}

func TestRateLimitedReaderWaitsBeforeReturningPayload(t *testing.T) {
	payload := buf.FromBytes([]byte("12345"))
	waited := time.Duration(0)
	limiter := newRateLimiter(1)
	limiter.now = func() time.Time {
		return time.Unix(0, 0).Add(waited)
	}
	limiter.sleep = func(ctx context.Context, d time.Duration) error {
		waited += d
		return nil
	}
	if err := limiter.wait(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	reader := &rateLimitedReader{
		ctx:     context.Background(),
		limiter: limiter,
		Reader:  &oneShotReader{mb: buf.MultiBuffer{payload}},
	}

	mb, err := reader.ReadMultiBuffer()
	if err != nil {
		t.Fatal(err)
	}
	defer buf.ReleaseMulti(mb)
	if mb.Len() != 5 {
		t.Fatalf("payload len = %d, want 5", mb.Len())
	}
	if waited <= 0 {
		t.Fatal("reader should wait instead of rejecting payload")
	}
}

func TestRateLimitedWriterWaitsThenForwardsPayload(t *testing.T) {
	waited := time.Duration(0)
	limiter := newRateLimiter(1)
	limiter.now = func() time.Time {
		return time.Unix(0, 0).Add(waited)
	}
	limiter.sleep = func(ctx context.Context, d time.Duration) error {
		waited += d
		return nil
	}
	if err := limiter.wait(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	capture := new(captureWriter)
	writer := &rateLimitedWriter{
		ctx:     context.Background(),
		limiter: limiter,
		Writer:  capture,
	}

	if err := writer.WriteMultiBuffer(buf.MultiBuffer{buf.FromBytes([]byte("12345"))}); err != nil {
		t.Fatal(err)
	}
	if waited <= 0 {
		t.Fatal("writer should wait instead of rejecting payload")
	}
	if capture.writes != 1 || capture.bytes != 5 {
		t.Fatalf("forwarded writes = %d bytes = %d, want one 5-byte write", capture.writes, capture.bytes)
	}
}
