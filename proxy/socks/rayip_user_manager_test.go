package socks

import (
	"context"
	"testing"

	"github.com/xtls/xray-core/common/protocol"
)

func TestRayIPSOCKSUserManagerUsesRuntimeEmail(t *testing.T) {
	server := &Server{
		config: &ServerConfig{
			AuthType:  AuthType_PASSWORD,
			UserLevel: 1,
		},
	}
	err := server.AddUser(context.Background(), &protocol.MemoryUser{
		Account: &Account{Username: "customer", Password: "secret"},
		Email:   "proxy-account-1",
		Level:   1,
	})
	if err != nil {
		t.Fatalf("AddUser() error = %v", err)
	}

	if !server.config.HasAccount("customer", "secret") {
		t.Fatal("HasAccount() = false, want true")
	}
	if email := server.config.RuntimeEmail("customer"); email != "proxy-account-1" {
		t.Fatalf("RuntimeEmail() = %q, want proxy-account-1", email)
	}

	if err := server.RemoveUser(context.Background(), "proxy-account-1"); err != nil {
		t.Fatalf("RemoveUser() error = %v", err)
	}
	if server.config.HasAccount("customer", "secret") {
		t.Fatal("HasAccount() after remove = true, want false")
	}
}
