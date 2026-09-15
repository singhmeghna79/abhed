package auth

import (
	"net/http/httptest"
	"testing"
	"time"
)

// Revocation has to end the session that exists, not only prevent the next
// sign-in. A revoked user who keeps working until their cookie expires has
// not been revoked.
func TestRevokeUserEndsLiveSessions(t *testing.T) {
	l := &LocalAuth{
		sessions:   map[string]*browserSession{},
		SessionTTL: time.Hour,
		CookieName: "abhed_session",
	}
	// Two sessions for the person being revoked, one for somebody else.
	l.issue(httptest.NewRecorder(), &User{Username: "evicted", Tenant: "default"})
	l.issue(httptest.NewRecorder(), &User{Username: "evicted", Tenant: "default"})
	l.issue(httptest.NewRecorder(), &User{Username: "bystander", Tenant: "default"})

	if got := len(l.sessions); got != 3 {
		t.Fatalf("setup: expected 3 sessions, got %d", got)
	}

	if n := l.RevokeUser("evicted"); n != 2 {
		t.Errorf("RevokeUser reported %d sessions ended, want 2", n)
	}
	if got := len(l.sessions); got != 1 {
		t.Errorf("%d sessions remain, want 1 (the bystander's)", got)
	}
	for _, s := range l.sessions {
		if s.Identity.Subject != "bystander" {
			t.Errorf("revoking one user ended another's session: %q", s.Identity.Subject)
		}
	}

	// Revoking somebody with no sessions is not an error, and must not touch
	// anyone else's.
	if n := l.RevokeUser("nobody"); n != 0 {
		t.Errorf("revoking an absent user reported %d", n)
	}
	if len(l.sessions) != 1 {
		t.Error("revoking an absent user disturbed the remaining session")
	}
	if n := l.RevokeUser(""); n != 0 {
		t.Errorf("revoking the empty username reported %d — that would match every session", n)
	}
}
