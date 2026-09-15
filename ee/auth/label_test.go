package auth

import "testing"

func TestProviderLabel(t *testing.T) {
	cases := []struct{ provider, issuer, want string }{
		{"google", "", "Google"},
		{"GOOGLE", "", "Google"},
		{"microsoft", "", "Microsoft"},
		{"", "https://keycloak.corp.example.com/realms/x", "keycloak.corp.example.com"},
		{"", "", "SSO"},
	}
	for _, c := range cases {
		if got := ProviderLabel(c.provider, c.issuer); got != c.want {
			t.Errorf("ProviderLabel(%q,%q) = %q, want %q",
				c.provider, c.issuer, got, c.want)
		}
	}
}
