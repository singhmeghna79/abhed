package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zybuu-ai/abhed/ee/schedule"
	ee "github.com/zybuu-ai/abhed/ee/server"
	"github.com/zybuu-ai/abhed/auth"
	"github.com/zybuu-ai/abhed/server"
	"github.com/zybuu-ai/abhed/server/servertest"
)

// These exercise the mounted surface — invites, access records, schedules —
// on a Community server built the way any edition builds one, through
// servertest. That is deliberate: a mount that only worked with unexported
// help would not be a mount.

func withMounts(o *server.Options) {
	sched, _ := schedule.New(nil, nil, nil)
	o.Mounts = []server.Mount{
		ee.AccessMount(ee.NewInvites(nil), nil),
		ee.SchedulesMount(sched),
	}
	o.AdminURL = "/admin"
}

// A mounted route is gated by the server's own admin group, so mounting can
// add a surface but cannot add a way past the gate. Both directions are
// tested deliberately: a gate that never denies is not a gate, and a gate
// that never admits is an outage.
func TestMountedAdminRoutesRequireTheAdminGroup(t *testing.T) {
	s := servertest.Proxy(t, withMounts)

	call := func(user, groups, method, path string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("X-Abhed-User", user)
		if groups != "" {
			req.Header.Set("X-Abhed-Groups", groups)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec.Code
	}

	routes := []struct{ method, path string }{
		{"GET", "/v1/admin/invites"},
		{"POST", "/v1/admin/invites"},
		// Access records name every person who ever asked, and revoke ends
		// somebody's account. An ordinary user reaching either would be a
		// disclosure and a denial-of-service respectively.
		{"GET", "/v1/admin/schedules"},
		{"POST", "/v1/admin/schedules/nightly/run"},
		{"GET", "/v1/admin/access"},
		{"GET", "/v1/admin/access/g-x/history"},
		{"POST", "/v1/admin/access/g-x/revoke"},
	}
	for _, rt := range routes {
		// Forbidden, specifically. A route that answers 501 to a non-admin
		// because its backing store is absent has not been authorised — it has
		// merely failed earlier, and would let the request through the moment
		// the store exists.
		if code := call("bob", "", rt.method, rt.path); code != http.StatusForbidden {
			t.Errorf("%s %s: non-admin got %d, want 403 — admin surface is open",
				rt.method, rt.path, code)
		}
		if code := call("alice", server.DefaultAdminGroup, rt.method, rt.path); code == http.StatusForbidden {
			t.Errorf("%s %s: admin was refused — the gate never admits",
				rt.method, rt.path)
		}
	}
}

// The dashboard page is served only when mounted, and the overview says so:
// a link to a page nobody registered would answer 404.
func TestAdminPageAndLinkExistOnlyWhenMounted(t *testing.T) {
	bare := servertest.New(t)
	rec := httptest.NewRecorder()
	bare.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/admin", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unmounted /admin answered %d", rec.Code)
	}

	mounted := servertest.New(t, withMounts)
	rec = httptest.NewRecorder()
	mounted.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/admin", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/v1/admin/access") {
		t.Errorf("mounted /admin: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mounted.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1/overview", nil))
	var o struct {
		AdminURL string `json:"admin_url"`
	}
	json.Unmarshal(rec.Body.Bytes(), &o)
	if o.AdminURL != "/admin" {
		t.Errorf("overview admin_url = %q, want /admin", o.AdminURL)
	}
}

// An invite is single-use. Two signups racing on one code must not both
// succeed, which is why redemption happens under the lock rather than as a
// check followed by a use.
func TestInviteIsSingleUseAndExpires(t *testing.T) {
	inv := ee.NewInvites(nil)
	ctx := context.Background()

	code := inv.Mint("alice", time.Hour).Code
	if err := inv.Redeem(ctx, code, "bob"); err != nil {
		t.Fatalf("first redemption failed: %v", err)
	}
	if err := inv.Redeem(ctx, code, "carol"); !errors.Is(err, ee.ErrInviteUsed) {
		t.Errorf("an invite was redeemed twice: %v", err)
	}
	if err := inv.Redeem(ctx, "not-a-code", "dave"); !errors.Is(err, ee.ErrInviteInvalid) {
		t.Errorf("an unknown code was accepted: %v", err)
	}

	old := inv.Mint("alice", -time.Minute).Code
	if err := inv.Redeem(ctx, old, "erin"); !errors.Is(err, ee.ErrInviteExpired) {
		t.Errorf("an expired invite was accepted: %v", err)
	}
}

// localWithInvites is a closed-registration server holding its own accounts,
// with an invite mechanism behind the signup handler.
func localWithInvites(t *testing.T) (*server.Server, *ee.Invites) {
	t.Helper()
	inv := ee.NewInvites(nil)
	local := auth.NewLocalAuth(auth.NewMemoryUserStore(), time.Hour, false)
	s := servertest.New(t, func(o *server.Options) {
		o.Config.Auth.Mode = "local"
		o.Config.Auth.AllowSignup = false
		o.Invites = inv
		o.Auth = &auth.Middleware{Providers: []auth.Provider{local},
			PublicPaths: []string{"/v1/signup", "/v1/overview"}}
	})
	return s, inv
}

// Invite-only is the middle ground between open and closed, and it must
// actually refuse a request with no code.
func TestSignupRequiresAnInviteWhenClosed(t *testing.T) {
	s, _ := localWithInvites(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/signup",
		strings.NewReader(`{"username":"mallory","password":"correct-horse-battery"}`)))
	if rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
		t.Fatalf("registration succeeded with no invite: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invite") {
		t.Errorf("the refusal does not say an invite is needed: %s", rec.Body.String())
	}
}

// Invite-only registration has to be REACHABLE. An earlier version gated the
// public path on allow_signup, which was right when the only two states were
// open and closed — and wrong once invites existed, because a valid code got a
// 401 from the middleware before the handler could read it.
//
// The distinction this pins: 401 means "the door is locked to you", 403 means
// "the door is here and you did not present a key". Only the second lets an
// invited user in.
func TestInviteSignupIsReachableWhenClosed(t *testing.T) {
	s, inv := localWithInvites(t)
	code := inv.Mint("admin", time.Hour).Code

	body := `{"username":"invited","password":"correct-horse-battery","invite":"` + code + `"}`
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/signup", strings.NewReader(body)))

	if rec.Code == http.StatusUnauthorized {
		t.Fatal("a valid invite was rejected by the auth layer before the " +
			"handler saw it — registration is unreachable")
	}
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("registration with a valid invite failed: %d %s", rec.Code, rec.Body.String())
	}
}

// The page cannot offer a door it is not told about. allow_signup alone cannot
// distinguish invite-only from closed, so an invite-only deployment looked shut
// to the people who had just been given codes.
func TestOverviewAnnouncesInviteSignup(t *testing.T) {
	s, _ := localWithInvites(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1/overview", nil))

	var o struct {
		AllowSignup  bool `json:"allow_signup"`
		InviteSignup bool `json:"invite_signup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	if o.AllowSignup {
		t.Error("closed registration reported as open")
	}
	if !o.InviteSignup {
		t.Error("invite registration not announced — the sign-in card will " +
			"hide a door that is open")
	}
}
