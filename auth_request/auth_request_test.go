package auth_request

import (
	"github.com/caddyserver/caddy/v2/caddytest"
	"testing"
)

func TestAuthRequest_ServeHTTP(t *testing.T) {
	tests := []struct {
		name               string
		config             string
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name: "Success",
			config: `
				{
					debug
					http_port 9080
					https_port 9443
					order auth_request before respond
				}
				:9080 {
					handle /auth {
						respond 200
					}
					auth_request {
						uri /auth
					}
					respond "ok"
				}`,
			expectedStatusCode: 200,
			expectedBody:       "ok",
		},
		{
			name: "Unauthorized",
			config: `
				{
					debug
					http_port 9080
					https_port 9443
					order auth_request before respond
				}
				:9080 {
					handle /auth {
						respond 401
					}
					auth_request {
						uri /auth
					}
					respond "ok"
				}`,
			expectedStatusCode: 401,
			expectedBody:       "",
		},
		{
			name: "Forbidden",
			config: `
				{
					debug
					http_port 9080
					https_port 9443
					order auth_request before respond
				}
				:9080 {
					handle /auth {
						respond 403
					}
					auth_request {
						uri /auth
					}
					respond "ok"
				}`,
			expectedStatusCode: 403,
			expectedBody:       "",
		},
		{
			name: "Bad Request",
			config: `
				{
					debug
					http_port 9080
					https_port 9443
					order auth_request before respond
				}
				:9080 {
					handle /auth {
						respond 400
					}
					auth_request {
						uri /auth
					}
					respond "ok"
				}`,
			expectedStatusCode: 500,
			expectedBody:       "",
		},
		{
			name: "Internal Server Error",
			config: `
				{
					debug
					http_port 9080
					https_port 9443
					order auth_request before respond
				}
				:9080 {
					handle /auth {
						respond 500
					}
					auth_request {
						uri /auth
					}
					respond "ok"
				}`,
			expectedStatusCode: 500,
			expectedBody:       "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tester := caddytest.NewTester(t)
			tester.InitServer(test.config, "caddyfile")
			tester.AssertGetResponse(
				"http://localhost:9080/",
				test.expectedStatusCode,
				test.expectedBody,
			)
		})
	}
}
