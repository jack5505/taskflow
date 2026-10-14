package httpserver

import "testing"

func TestRedactPath(t *testing.T) {
	for in, want := range map[string]string{
		"/api/v1/invites/abc":          "/api/v1/invites/***",
		"/api/v1/invites/abc/register": "/api/v1/invites/***/register",
		"/join/abc":                    "/join/***",
		"/api/v1/projects/TM/invites":  "/api/v1/projects/TM/invites",
		"/password-set":                "/password-set",
	} {
		if got := redactPath(in); got != want {
			t.Errorf("redactPath(%q) = %q, want %q", in, got, want)
		}
	}
}
