package rayipruntime

import (
	"testing"
	"time"
)

func TestConnectionLimitReleasesAfterClose(t *testing.T) {
	manager := NewManager()
	manager.SetPolicy(AccountPolicy{
		Email:          "acct-1",
		MaxConnections: 1,
		Priority:       1,
	})

	release, err := manager.AcquireConnection("acct-1")
	if err != nil {
		t.Fatalf("AcquireConnection() first error = %v", err)
	}
	if _, err := manager.AcquireConnection("acct-1"); err == nil {
		t.Fatal("AcquireConnection() second error = nil, want connection limit error")
	}
	release()

	if _, err := manager.AcquireConnection("acct-1"); err != nil {
		t.Fatalf("AcquireConnection() after release error = %v", err)
	}
}

func TestFixedRateLimitUsesBytesPerSecondTokenBucket(t *testing.T) {
	manager := NewManager()
	now := time.Unix(100, 0)
	manager.SetPolicy(AccountPolicy{
		Email:           "acct-1",
		EgressLimitBPS:  100,
		IngressLimitBPS: 200,
		Priority:        1,
	})

	if allowed := manager.AllowBytesAt("acct-1", DirectionEgress, 80, now); allowed != 80 {
		t.Fatalf("first egress allowed = %d, want 80", allowed)
	}
	if allowed := manager.AllowBytesAt("acct-1", DirectionEgress, 80, now); allowed != 20 {
		t.Fatalf("same-second egress allowed = %d, want remaining 20", allowed)
	}
	if allowed := manager.AllowBytesAt("acct-1", DirectionEgress, 100, now.Add(500*time.Millisecond)); allowed != 50 {
		t.Fatalf("half-second refill allowed = %d, want 50", allowed)
	}
	if allowed := manager.AllowBytesAt("acct-1", DirectionIngress, 200, now); allowed != 200 {
		t.Fatalf("ingress allowed = %d, want 200", allowed)
	}
}

func TestSmartFairLimitUsesPriorityAndShortTermConsumption(t *testing.T) {
	manager := NewManager()
	manager.SetFairPool(300)
	now := time.Unix(100, 0)
	manager.SetPolicy(AccountPolicy{Email: "low", Priority: 1})
	manager.SetPolicy(AccountPolicy{Email: "high", Priority: 3})

	lowShare := manager.FairShareBPS("low", DirectionEgress, now)
	highShare := manager.FairShareBPS("high", DirectionEgress, now)
	if lowShare != 75 || highShare != 225 {
		t.Fatalf("shares = low %d high %d, want 75 and 225", lowShare, highShare)
	}

	manager.RecordTrafficAt("high", DirectionEgress, 450, now)
	penalized := manager.FairShareBPS("high", DirectionEgress, now.Add(time.Second))
	if penalized >= highShare {
		t.Fatalf("penalized high share = %d, want below %d", penalized, highShare)
	}
}

func TestAbuseDetectionReportsWithoutLocalDisable(t *testing.T) {
	manager := NewManager()
	now := time.Unix(100, 0)
	manager.SetPolicy(AccountPolicy{
		Email:              "acct-1",
		Priority:           1,
		AbuseBytesPerMin:   1000,
		AbuseDisablePolicy: DisableAndReport,
	})

	event := manager.RecordTrafficAt("acct-1", DirectionEgress, 1200, now)
	if event == nil {
		t.Fatal("RecordTrafficAt() event = nil, want abuse event")
	}
	if event.Action != DisableAndReport || event.Email != "acct-1" {
		t.Fatalf("unexpected abuse event: %#v", event)
	}
	policy, ok := manager.Policy("acct-1")
	if !ok || policy.Disabled {
		t.Fatalf("policy disabled = %v ok = %v, want cloud-control-only disable", policy.Disabled, ok)
	}
}

func TestDigestChangesWithPoliciesAndGeneration(t *testing.T) {
	manager := NewManager()
	manager.SetPolicy(AccountPolicy{Email: "acct-1", Priority: 1, Generation: 7})
	digest := manager.Digest()

	if digest.AccountCount != 1 || digest.MaxGeneration != 7 || digest.Hash == "" {
		t.Fatalf("unexpected digest: %#v", digest)
	}
}
