package command

import (
	"context"
	"math"

	"github.com/xtls/xray-core/app/rayipruntime"
	"github.com/xtls/xray-core/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const extensionABI = "rayip.runtime.v1"

var capabilities = []string{
	"socks5",
	"http",
	"rayip-runtime",
	"account-rate-limit",
	"smart-fair-limit",
	"congestion-aware-fair-limit",
	"connection-limit",
	"usage-stats",
	"runtime-digest",
	"abuse-detection",
}

type runtimeServer struct {
	UnimplementedRuntimeServiceServer
	manager *rayipruntime.Manager
}

func NewRuntimeServer(manager *rayipruntime.Manager) RuntimeServiceServer {
	return &runtimeServer{manager: manager}
}

func (s *runtimeServer) GetCapabilities(context.Context, *GetCapabilitiesRequest) (*GetCapabilitiesResponse, error) {
	return &GetCapabilitiesResponse{
		ExtensionAbi: extensionABI,
		Capabilities: append([]string(nil), capabilities...),
		Digest:       toDigest(s.manager.Digest()),
	}, nil
}

func (s *runtimeServer) UpsertAccountPolicy(_ context.Context, request *UpsertAccountPolicyRequest) (*UpsertAccountPolicyResponse, error) {
	policy, err := fromPolicy(request.GetPolicy())
	if err != nil {
		return nil, err
	}
	s.manager.SetPolicy(policy)
	return &UpsertAccountPolicyResponse{Digest: toDigest(s.manager.Digest())}, nil
}

func (s *runtimeServer) RemoveAccountPolicy(_ context.Context, request *RemoveAccountPolicyRequest) (*RemoveAccountPolicyResponse, error) {
	if request.GetEmail() == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}
	s.manager.RemovePolicy(request.GetEmail())
	return &RemoveAccountPolicyResponse{Digest: toDigest(s.manager.Digest())}, nil
}

func (s *runtimeServer) SetUserRateLimit(_ context.Context, request *SetUserRateLimitRequest) (*SetUserRateLimitResponse, error) {
	policy, ok := s.manager.Policy(request.GetEmail())
	if !ok {
		return nil, status.Error(codes.NotFound, "account policy not found")
	}
	policy.EgressLimitBPS = uintToInt64(request.GetEgressLimitBps())
	policy.IngressLimitBPS = uintToInt64(request.GetIngressLimitBps())
	s.manager.SetPolicy(policy)
	return &SetUserRateLimitResponse{
		Policy: toPolicy(policy),
		Digest: toDigest(s.manager.Digest()),
	}, nil
}

func (s *runtimeServer) RemoveUserRateLimit(_ context.Context, request *RemoveUserRateLimitRequest) (*RemoveUserRateLimitResponse, error) {
	policy, ok := s.manager.Policy(request.GetEmail())
	if !ok {
		return nil, status.Error(codes.NotFound, "account policy not found")
	}
	policy.EgressLimitBPS = 0
	policy.IngressLimitBPS = 0
	s.manager.SetPolicy(policy)
	return &RemoveUserRateLimitResponse{
		Policy: toPolicy(policy),
		Digest: toDigest(s.manager.Digest()),
	}, nil
}

func (s *runtimeServer) GetUserSpeed(_ context.Context, request *GetUserSpeedRequest) (*GetUserSpeedResponse, error) {
	if request.GetEmail() == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}
	return &GetUserSpeedResponse{Speed: toSpeed(s.manager.Usage(request.GetEmail()))}, nil
}

func (s *runtimeServer) ListUserSpeeds(context.Context, *ListUserSpeedsRequest) (*ListUserSpeedsResponse, error) {
	items := s.manager.ListUsage()
	response := &ListUserSpeedsResponse{Speeds: make([]*UserSpeed, 0, len(items))}
	for _, usage := range items {
		response.Speeds = append(response.Speeds, toSpeed(usage))
	}
	return response, nil
}

func (s *runtimeServer) SetFairPool(_ context.Context, request *SetFairPoolRequest) (*SetFairPoolResponse, error) {
	bytesPerSecond := uintToInt64(request.GetBytesPerSecond())
	s.manager.SetFairPool(bytesPerSecond)
	return &SetFairPoolResponse{
		BytesPerSecond: intToUint64(bytesPerSecond),
		Digest:         toDigest(s.manager.Digest()),
	}, nil
}

func (s *runtimeServer) SetFairnessState(_ context.Context, request *SetFairnessStateRequest) (*SetFairnessStateResponse, error) {
	state := rayipruntime.FairnessState{
		EgressPoolBPS:     uintToInt64(request.GetEgressPoolBps()),
		IngressPoolBPS:    uintToInt64(request.GetIngressPoolBps()),
		WindowSeconds:     int64(request.GetWindowSeconds()),
		LossRatePPM:       int64(request.GetLossRatePpm()),
		RetransmitRatePPM: int64(request.GetRetransmitRatePpm()),
		TargetLossPPM:     int64(request.GetTargetLossPpm()),
		TargetRetransPPM:  int64(request.GetTargetRetransmitPpm()),
		MinCongestionBPS:  uintToInt64(request.GetMinCongestionBps()),
	}
	s.manager.SetFairnessState(state)
	return &SetFairnessStateResponse{
		EgressPoolBps:  intToUint64(state.EgressPoolBPS),
		IngressPoolBps: intToUint64(state.IngressPoolBPS),
		Digest:         toDigest(s.manager.Digest()),
	}, nil
}

func (s *runtimeServer) GetDigest(context.Context, *GetDigestRequest) (*GetDigestResponse, error) {
	return &GetDigestResponse{Digest: toDigest(s.manager.Digest())}, nil
}

type service struct {
	manager *rayipruntime.Manager
}

func (s *service) Register(server *grpc.Server) {
	RegisterRuntimeServiceServer(server, NewRuntimeServer(s.manager))
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(context.Context, interface{}) (interface{}, error) {
		return &service{manager: rayipruntime.DefaultManager()}, nil
	}))
}

func fromPolicy(policy *AccountPolicy) (rayipruntime.AccountPolicy, error) {
	if policy == nil {
		return rayipruntime.AccountPolicy{}, status.Error(codes.InvalidArgument, "policy is required")
	}
	if policy.GetEmail() == "" {
		return rayipruntime.AccountPolicy{}, status.Error(codes.InvalidArgument, "email is required")
	}
	return rayipruntime.AccountPolicy{
		Email:              policy.GetEmail(),
		EgressLimitBPS:     uintToInt64(policy.GetEgressLimitBps()),
		IngressLimitBPS:    uintToInt64(policy.GetIngressLimitBps()),
		MaxConnections:     int(policy.GetMaxConnections()),
		Priority:           int(policy.GetPriority()),
		Generation:         policy.GetGeneration(),
		Disabled:           policy.GetDisabled(),
		AbuseBytesPerMin:   uintToInt64(policy.GetAbuseBytesPerMin()),
		AbuseDisablePolicy: fromAbuseAction(policy.GetAbuseAction()),
	}, nil
}

func toPolicy(policy rayipruntime.AccountPolicy) *AccountPolicy {
	return &AccountPolicy{
		Email:            policy.Email,
		EgressLimitBps:   intToUint64(policy.EgressLimitBPS),
		IngressLimitBps:  intToUint64(policy.IngressLimitBPS),
		MaxConnections:   uint32(policy.MaxConnections),
		Priority:         uint32(policy.Priority),
		Generation:       policy.Generation,
		Disabled:         policy.Disabled,
		AbuseBytesPerMin: intToUint64(policy.AbuseBytesPerMin),
		AbuseAction:      toAbuseAction(policy.AbuseDisablePolicy),
	}
}

func toSpeed(usage rayipruntime.Usage) *UserSpeed {
	return &UserSpeed{
		Email:             usage.Email,
		RxBytes:           intToUint64(usage.RxBytes),
		TxBytes:           intToUint64(usage.TxBytes),
		ActiveConnections: uint64(usage.ActiveConnections),
	}
}

func toDigest(digest rayipruntime.Digest) *Digest {
	return &Digest{
		AccountCount:  digest.AccountCount,
		EnabledCount:  digest.EnabledCount,
		DisabledCount: digest.DisabledCount,
		MaxGeneration: digest.MaxGeneration,
		Hash:          digest.Hash,
	}
}

func fromAbuseAction(action AbuseAction) rayipruntime.AbuseAction {
	switch action {
	case AbuseAction_ABUSE_ACTION_DISABLE_AND_REPORT:
		return rayipruntime.DisableAndReport
	default:
		return rayipruntime.ReportOnly
	}
}

func toAbuseAction(action rayipruntime.AbuseAction) AbuseAction {
	switch action {
	case rayipruntime.DisableAndReport:
		return AbuseAction_ABUSE_ACTION_DISABLE_AND_REPORT
	case rayipruntime.ReportOnly:
		return AbuseAction_ABUSE_ACTION_REPORT_ONLY
	default:
		return AbuseAction_ABUSE_ACTION_UNSPECIFIED
	}
}

func uintToInt64(value uint64) int64 {
	if value > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(value)
}

func intToUint64(value int64) uint64 {
	if value < 0 {
		return 0
	}
	return uint64(value)
}
