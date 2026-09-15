package server

import (
	"regexp"
	"strings"
	"testing"
)

// An invite that becomes an account has to say so to whatever issued it, or
// a dashboard would be offering to revoke access it never saw granted. The
// issuer is behind Options.Invites; this pins that signup tells it, and tells
// it only once the account exists.
func TestRedeemingLinksTheAccountToItsGrant(t *testing.T) {
	src := readSource(t, "server.go")
	fn := regexp.MustCompile(`(?s)func \(s \*Server\) signup\(.*?\n\}`).FindString(src)
	if fn == "" {
		t.Fatal("cannot find signup")
	}
	if !strings.Contains(fn, "Invites.Redeemed") {
		t.Error("signup does not tell the invite issuer which account the code " +
			"became, so a dashboard would show a grant with no username against it")
	}
	// Order matters: link after the account exists.
	iCreate := strings.Index(fn, "local.CreateUser")
	iLink := strings.Index(fn, "Invites.Redeemed")
	if iCreate < 0 || iLink < 0 || iLink < iCreate {
		t.Error("the grant is linked before the account is created — a failed " +
			"CreateUser would leave a grant naming an account that does not exist")
	}
}
