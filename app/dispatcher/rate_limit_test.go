package dispatcher

import "testing"

func TestNewByteRateLimiterConvertsBitsToBytes(t *testing.T) {
	limiter := newByteRateLimiter(1_000_000)
	if limiter == nil {
		t.Fatal("expected limiter")
	}
	if limiter.rate != 125_000 {
		t.Fatalf("unexpected byte rate: got=%v want=125000", limiter.rate)
	}
	if limiter.burst != 125_000 {
		t.Fatalf("unexpected burst: got=%v want=125000", limiter.burst)
	}
}

func TestNewByteRateLimiterRejectsNonPositive(t *testing.T) {
	if limiter := newByteRateLimiter(0); limiter != nil {
		t.Fatal("expected nil limiter for zero rate")
	}
	if limiter := newByteRateLimiter(-1); limiter != nil {
		t.Fatal("expected nil limiter for negative rate")
	}
}
