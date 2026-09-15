package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The dashboard can read grants and revoke them. It was built before anything
// wrote one, which meant an invite could become an account with no record
// behind it — and the dashboard would then be offering to revoke access it had
// never seen granted. This asserts the write path exists at the issuing end;
// the redeeming end is the Community signup handler, pinned in its own
// package.
//
// It reads the source rather than exercising the handler on purpose: the
// dangerous change is a call going missing, which a fixture with a nil access
// store cannot notice.
func TestIssuingAnInviteRecordsAGrant(t *testing.T) {
	src := readSource(t, "access.go")
	fn := regexp.MustCompile(`(?s)func \(a \*accessAPI\) createInvite\(.*?\n\}`).FindString(src)
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
	if !strings.Contains(fn, "log.Error(\"record grant\"") {
		t.Error("createInvite does not log a recording failure — it must " +
			"continue and say so, not fail the request")
	}
}

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("cannot read %s, so nothing was checked: %v", name, err)
	}
	return string(b)
}
