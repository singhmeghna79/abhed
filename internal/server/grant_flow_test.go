package server

import (
	"regexp"
	"strings"
	"testing"
)

// The dashboard can read grants and revoke them. It was built before anything
// wrote one, which meant an invite could become an account with no record
// behind it — and the dashboard would then be offering to revoke access it had
// never seen granted. These assert the write path exists at both ends.
func TestIssuingAnInviteRecordsAGrant(t *testing.T) {
	src := readSource(t, "admin.go")
	fn := regexp.MustCompile(`(?s)func \(s \*Server\) createInvite\(.*?\n\}`).FindString(src)
	if fn == "" {
		t.Fatal("cannot find createInvite")
	}
	for _, want := range []string{"RecordRequest", "GrantAccess"} {
		if !strings.Contains(fn, want) {
			t.Errorf("createInvite does not call %s — an invite issued from the "+
				"admin API would not appear on the dashboard, and access could "+
				"be granted with no record of granting it", want)
		}
	}
	// Recording must not be able to break issuing. An invite that failed to
	// mint because the database was busy is a worse outcome than an invite
	// with no paperwork.
	if !strings.Contains(fn, "s.log.Error(\"record grant\"") {
		t.Error("createInvite does not log a recording failure — it must " +
			"continue and say so, not fail the request")
	}
}

func TestRedeemingLinksTheAccountToItsGrant(t *testing.T) {
	src := readSource(t, "server.go")
	fn := regexp.MustCompile(`(?s)func \(s \*Server\) signup\(.*?\n\}`).FindString(src)
	if fn == "" {
		t.Fatal("cannot find signup")
	}
	if !strings.Contains(fn, "Access.Redeemed") {
		t.Error("signup does not link the new account to its grant, so the " +
			"dashboard would show a grant with no username against it")
	}
	// Order matters: link after the account exists.
	iCreate := strings.Index(fn, "local.CreateUser")
	iLink := strings.Index(fn, "Access.Redeemed")
	if iCreate < 0 || iLink < 0 || iLink < iCreate {
		t.Error("the grant is linked before the account is created — a failed " +
			"CreateUser would leave a grant naming an account that does not exist")
	}
}
