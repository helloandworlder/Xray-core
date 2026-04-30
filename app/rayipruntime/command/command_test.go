package command

import (
	"context"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/rayipruntime"
)

func TestRuntimeServiceCapabilitiesAndDigest(t *testing.T) {
	server := NewRuntimeServer(rayipruntime.NewManager())

	capabilities, err := server.GetCapabilities(context.Background(), &GetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetCapabilities() error = %v", err)
	}
	if capabilities.GetExtensionAbi() != extensionABI {
		t.Fatalf("extension abi = %q, want %q", capabilities.GetExtensionAbi(), extensionABI)
	}

	if _, err := server.UpsertAccountPolicy(context.Background(), &UpsertAccountPolicyRequest{
		Policy: &AccountPolicy{
			Email:            "acct-1",
			EgressLimitBps:   100,
			IngressLimitBps:  200,
			MaxConnections:   2,
			Priority:         3,
			Generation:       7,
			AbuseBytesPerMin: 1000,
			AbuseAction:      AbuseAction_ABUSE_ACTION_DISABLE_AND_REPORT,
		},
	}); err != nil {
		t.Fatalf("UpsertAccountPolicy() error = %v", err)
	}

	digest, err := server.GetDigest(context.Background(), &GetDigestRequest{})
	if err != nil {
		t.Fatalf("GetDigest() error = %v", err)
	}
	if digest.GetDigest().GetAccountCount() != 1 || digest.GetDigest().GetMaxGeneration() != 7 {
		t.Fatalf("unexpected digest: %#v", digest.GetDigest())
	}
}

func TestRuntimeServiceRateLimitUpdate(t *testing.T) {
	manager := rayipruntime.NewManager()
	manager.SetPolicy(rayipruntime.AccountPolicy{Email: "acct-1", Priority: 1})
	server := NewRuntimeServer(manager)

	response, err := server.SetUserRateLimit(context.Background(), &SetUserRateLimitRequest{
		Email:           "acct-1",
		EgressLimitBps:  1024,
		IngressLimitBps: 2048,
	})
	if err != nil {
		t.Fatalf("SetUserRateLimit() error = %v", err)
	}
	if response.GetPolicy().GetEgressLimitBps() != 1024 || response.GetPolicy().GetIngressLimitBps() != 2048 {
		t.Fatalf("unexpected policy after set: %#v", response.GetPolicy())
	}

	response2, err := server.RemoveUserRateLimit(context.Background(), &RemoveUserRateLimitRequest{Email: "acct-1"})
	if err != nil {
		t.Fatalf("RemoveUserRateLimit() error = %v", err)
	}
	if response2.GetPolicy().GetEgressLimitBps() != 0 || response2.GetPolicy().GetIngressLimitBps() != 0 {
		t.Fatalf("unexpected policy after remove: %#v", response2.GetPolicy())
	}
}

func TestRuntimeServiceFairPoolUpdate(t *testing.T) {
	manager := rayipruntime.NewManager()
	manager.SetPolicy(rayipruntime.AccountPolicy{Email: "low", Priority: 1})
	manager.SetPolicy(rayipruntime.AccountPolicy{Email: "high", Priority: 3})
	server := NewRuntimeServer(manager)

	response, err := server.SetFairPool(context.Background(), &SetFairPoolRequest{BytesPerSecond: 400})
	if err != nil {
		t.Fatalf("SetFairPool() error = %v", err)
	}
	if response.GetBytesPerSecond() != 400 {
		t.Fatalf("bytes_per_second = %d, want 400", response.GetBytesPerSecond())
	}
	if share := manager.FairShareBPS("high", rayipruntime.DirectionEgress, time.Unix(100, 0)); share != 300 {
		t.Fatalf("high fair share = %d, want 300", share)
	}
}

func TestRuntimeServiceFairnessStateUpdate(t *testing.T) {
	manager := rayipruntime.NewManager()
	manager.SetPolicy(rayipruntime.AccountPolicy{Email: "low", Priority: 1})
	manager.SetPolicy(rayipruntime.AccountPolicy{Email: "high", Priority: 3})
	server := NewRuntimeServer(manager)

	_, err := server.SetFairnessState(context.Background(), &SetFairnessStateRequest{
		EgressPoolBps:       800,
		IngressPoolBps:      600,
		WindowSeconds:       300,
		LossRatePpm:         30000,
		RetransmitRatePpm:   50000,
		TargetLossPpm:       5000,
		TargetRetransmitPpm: 10000,
	})
	if err != nil {
		t.Fatalf("SetFairnessState() error = %v", err)
	}
	if share := manager.FairShareBPS("high", rayipruntime.DirectionEgress, time.Unix(100, 0)); share >= 600 {
		t.Fatalf("high fair share = %d, want congestion-compressed below uncongested share", share)
	}
}
