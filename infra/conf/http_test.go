package conf_test

import (
	"testing"

	"github.com/xtls/xray-core/common/protocol"
	. "github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/http"
)

func TestHTTPServerConfig(t *testing.T) {
	creator := func() Buildable {
		return new(HTTPServerConfig)
	}

	runMultiTestCase(t, []TestCase{
		{
			Input: `{
				"accounts": [
					{
						"user": "my-username",
						"pass": "my-password"
					}
				],
				"allowTransparent": true,
				"userLevel": 1
			}`,
			Parser: loadJSON(creator),
			Output: &http.ServerConfig{
				Accounts: map[string]string{
					"my-username": "my-password",
				},
				AllowTransparent: true,
				UserLevel:        1,
			},
		},
		{
			Input: `{
				"accounts": [
					{
						"user": "limited-user",
						"pass": "my-password",
						"email": "limited@example",
						"level": 2,
						"uplinkLimitBps": 1024,
						"downlinkLimitBps": 2048,
						"maxConnections": 3
					}
				],
				"userLevel": 1
			}`,
			Parser: loadJSON(creator),
			Output: &http.ServerConfig{
				Accounts: map[string]string{
					"limited-user": "my-password",
				},
				UserLevel: 1,
				AccountUsers: map[string]*protocol.User{
					"limited-user": {
						Email:            "limited@example",
						Level:            2,
						UplinkLimitBps:   1024,
						DownlinkLimitBps: 2048,
						MaxConnections:   3,
					},
				},
			},
		},
	})
}
