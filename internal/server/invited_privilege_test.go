package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// readSource reads a file from this package for assertions about the code
// itself, used where the dangerous change is a literal rather than a
// behaviour a fixture would exercise.
func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("cannot read %s, so nothing was checked: %v", name, err)
	}
	return string(b)
}

// An invited account must be an ordinary user, always.
//
// The whole access model rests on this: strangers get a temporary account that
// can run the agent and nothing else, and administration stays with the person
// who owns the machine. If signup ever put a group on a new account — copied
// from a config default, inherited from the inviter, or set for convenience
// during debugging — every guarantee above it would be void, and the failure
// would be silent because the account would work normally right up until it
// did something only an admin can do.
//
// This reads the source rather than exercising a handler on purpose. The
// dangerous change is a literal appearing in one struct, and that is exactly
// what a source assertion catches and a behavioural test can miss when the
// fixture happens not to configure groups.
func TestInvitedAccountsGetNoGroups(t *testing.T) {
	src := readSource(t, "server.go")

	// The auth.User composed by signup.
	m := regexp.MustCompile(`(?s)u := auth\.User\{.*?\n\t\}`).FindString(src)
	if m == "" {
		t.Fatal("cannot find the auth.User built by signup — if account " +
			"creation moved, move this test with it rather than deleting it")
	}
	if strings.Contains(m, "Groups") {
		t.Errorf("signup sets Groups on a new account:\n%s\n\n"+
			"An invited account must carry no groups. Admin is group "+
			"membership, so a group here is an admin account handed to a "+
			"stranger who filled in a web form.", m)
	}

	// And the promotion path stays admin-only, so the only way to become an
	// admin is for an existing admin to say so.
	routes := readSource(t, "server.go")
	if !strings.Contains(routes, `mux.Handle("POST /v1/admin/users/admin", s.admin(`) {
		t.Error("the promote-to-admin route is not wrapped in s.admin() — " +
			"anyone signed in could grant themselves administration")
	}
}

// The bootstrap admin is the operator's own account and must not be reachable
// through any public path.
func TestSignupCannotCreateAnAdmin(t *testing.T) {
	src := readSource(t, "server.go")
	sig := regexp.MustCompile(`(?s)func \(s \*Server\) signup\(.*?\n\}`).FindString(src)
	if sig == "" {
		t.Fatal("cannot find the signup handler")
	}
	for _, forbidden := range []string{"AdminGroup", "adminGroup()", "titan-admin"} {
		if strings.Contains(sig, forbidden) {
			t.Errorf("signup references %q — the public signup path must have "+
				"no way to reach the admin group", forbidden)
		}
	}
}
