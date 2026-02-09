package command

import (
	"context"
	"sort"
	"strings"

	"github.com/xtls/xray-core/app/userratelimit"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"google.golang.org/grpc"
)

type userRateLimitServer struct {
	UnimplementedUserRateLimitServiceServer
}

func (s *userRateLimitServer) SetUserRateLimit(_ context.Context, req *SetUserRateLimitRequest) (*SetUserRateLimitResponse, error) {
	if req == nil {
		return nil, errors.New("nil request")
	}
	email := strings.TrimSpace(req.Email)
	if email == "" {
		return nil, errors.New("email is required")
	}
	if !userratelimit.Set(email, req.GetUplinkBps(), req.GetDownlinkBps()) {
		return nil, errors.New("email is required")
	}
	return &SetUserRateLimitResponse{}, nil
}

func (s *userRateLimitServer) RemoveUserRateLimit(_ context.Context, req *RemoveUserRateLimitRequest) (*RemoveUserRateLimitResponse, error) {
	if req == nil {
		return nil, errors.New("nil request")
	}
	email := strings.TrimSpace(req.Email)
	if email == "" {
		return nil, errors.New("email is required")
	}
	userratelimit.Delete(email)
	return &RemoveUserRateLimitResponse{}, nil
}

func (s *userRateLimitServer) GetUserRateLimit(_ context.Context, req *GetUserRateLimitRequest) (*GetUserRateLimitResponse, error) {
	if req == nil {
		return nil, errors.New("nil request")
	}
	email := strings.TrimSpace(req.Email)
	if email == "" {
		return nil, errors.New("email is required")
	}
	item, ok := userratelimit.Get(email)
	if !ok {
		return &GetUserRateLimitResponse{Exists: false}, nil
	}
	return &GetUserRateLimitResponse{Exists: true, UplinkBps: item.UplinkBps, DownlinkBps: item.DownlinkBps}, nil
}

func (s *userRateLimitServer) ListUserRateLimits(_ context.Context, _ *ListUserRateLimitsRequest) (*ListUserRateLimitsResponse, error) {
	items := userratelimit.List()
	keys := make([]string, 0, len(items))
	for email := range items {
		keys = append(keys, email)
	}
	sort.Strings(keys)
	resp := &ListUserRateLimitsResponse{Items: make([]*UserRateLimitItem, 0, len(keys))}
	for _, email := range keys {
		item := items[email]
		resp.Items = append(resp.Items, &UserRateLimitItem{Email: email, UplinkBps: item.UplinkBps, DownlinkBps: item.DownlinkBps})
	}
	return resp, nil
}

type service struct{}

func (s *service) Register(server *grpc.Server) {
	RegisterUserRateLimitServiceServer(server, &userRateLimitServer{})

	vCoreDesc := UserRateLimitService_ServiceDesc
	vCoreDesc.ServiceName = "v2ray.core.app.userratelimit.command.UserRateLimitService"
	server.RegisterService(&vCoreDesc, &userRateLimitServer{})
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, cfg interface{}) (interface{}, error) {
		return &service{}, nil
	}))
}
