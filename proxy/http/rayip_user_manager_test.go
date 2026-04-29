package http

import (
	"context"
	"testing"

	"github.com/xtls/xray-core/common/protocol"
)

func TestRayIPHTTPUserManagerUsesRuntimeEmail(t *testing.T) {
	server := &Server{config: &ServerConfig{UserLevel: 1}}
	err := server.AddUser(context.Background(), &protocol.MemoryUser{
		Account: &Account{Username: "customer", Password: "secret"},
		Email:   "proxy-account-1",
		Level:   1,
	})
	if err != nil {
		t.Fatalf("AddUser() error = %v", err)
	}

	email, ok := server.authenticate("customer", "secret")
	if !ok || email != "proxy-account-1" {
		t.Fatalf("authenticate() = %q %v, want proxy-account-1 true", email, ok)
	}
	if got := server.GetUser(context.Background(), "proxy-account-1"); got == nil || got.Email != "proxy-account-1" {
		t.Fatalf("GetUser() = %#v", got)
	}

	if err := server.RemoveUser(context.Background(), "proxy-account-1"); err != nil {
		t.Fatalf("RemoveUser() error = %v", err)
	}
	if _, ok := server.authenticate("customer", "secret"); ok {
		t.Fatal("authenticate() after remove ok = true, want false")
	}
}
