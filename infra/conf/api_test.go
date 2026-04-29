package conf_test

import (
	"testing"

	"github.com/xtls/xray-core/app/rayipruntime/command"
	. "github.com/xtls/xray-core/infra/conf"
)

func TestAPIConfigBuildsRayIPRuntimeService(t *testing.T) {
	cfg, err := (&APIConfig{
		Tag:      "api",
		Listen:   "127.0.0.1:10085",
		Services: []string{"RayIPRuntimeService"},
	}).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(cfg.Service) != 1 {
		t.Fatalf("service count = %d, want 1", len(cfg.Service))
	}
	instance, err := cfg.Service[0].GetInstance()
	if err != nil {
		t.Fatalf("GetInstance() error = %v", err)
	}
	if _, ok := instance.(*command.Config); !ok {
		t.Fatalf("service instance = %T, want *command.Config", instance)
	}
}
