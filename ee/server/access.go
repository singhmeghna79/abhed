// Package server is what the enterprise edition mounts on the Community
// server: invites and access records, the dashboard that fronts them, and
// the scheduler's admin view.
//
// Everything here reaches the Community server through what it exports — a
// Mount, the Admin gate, the JSON writers, StartSession — and nothing else.
// That is the boundary this edition is built on, and it is checked the hard
// way: the Community binary's own test fails if any package under ee/ is in
// its dependency graph.
package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zybuu-ai/abhed/ee/store"
	"github.com/zybuu-ai/abhed/server"
)

// Invites and access records: who was given a key, who has one, who had one.
//
// None of this is wired into the Community server directly. It is a Mount,
// and the signup handler reaches it only through Options.Invites, because the
// questions it answers — who may register here, and what record stands behind
// them — belong to whoever operates a deployment for people they do not
// already know. A single-tenant server for one team has no such people; its
// accounts are created by the operator and that is the whole access policy.

// AccessStore is what the dashboard needs to answer "who has access, who
// asked, and who used to". An access decision has to outlive a restart, so
// it is durable storage or it is nothing.
type AccessStore interface {
	RecordRequest(ctx context.Context, g store.Grant) (store.Grant, error)
	GrantAccess(ctx context.Context, id, by, code string, expires time.Time) error
	Revoke(ctx context.Context, id, by, clause, note string) (store.Grant, error)
	GrantByID(ctx context.Context, id string) (store.Grant, error)
	GrantByUsername(ctx context.Context, username string) (store.Grant, error)
	Grants(ctx context.Context) ([]store.Grant, error)
	AccessHistory(ctx context.Context, id string) ([]store.AccessEvent, error)
	Redeemed(ctx context.Context, code, username string) error
}

// ---------------------------------------------------------------- invites

// Signup was disabled outright because Abhed runs shell commands, and open
// registration on a public URL hands a stranger an agent with a shell. That is
// still true — but "no signup at all" was blocking adoption, and the answer is
// not to open the door, it is to decide who gets a key.
//
// An invite is single-use and expires. New accounts land with no groups, so an
// invited user is an ordinary user until an administrator promotes them.
type Invite struct {
	Code      string    `json:"code"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	UsedBy    string    `json:"used_by,omitempty"`
	UsedAt    time.Time `json:"used_at,omitempty"`
}

// Invites keeps outstanding invites in memory and, when there is an access
// store, links each redeemed code to the grant it was issued for. It is the
// Community server's InviteRedeemer.
//
// Deliberately not durable: an invite is short-lived by design, and losing the
// outstanding ones on restart is a mild inconvenience, where persisting them
// would put a credential-shaped secret in the store for no gain. The GRANT is
// durable; the code is not.
type Invites struct {
	mu   sync.Mutex
	byID map[string]*Invite
	// access may be nil: an invite minted with no record behind it — by the
	// CLI, say — is still an invite, it simply links to nothing.
	access AccessStore
}

// NewInvites creates an empty invite store over an optional access store.
func NewInvites(access AccessStore) *Invites {
	return &Invites{byID: map[string]*Invite{}, access: access}
}

// Mint issues a single-use invite that expires after ttl.
func (i *Invites) Mint(by string, ttl time.Duration) *Invite {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("server: crypto/rand unavailable: " + err.Error())
	}
	code := strings.ToLower(base32.StdEncoding.WithPadding(
		base32.NoPadding).EncodeToString(b[:]))

	inv := &Invite{
		Code: code, CreatedBy: by,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.byID[code] = inv
	return inv
}

// Redeem consumes an invite, or reports why it cannot be used.
//
// Single-use is enforced here, under the lock, rather than by the caller
// checking and then using — two signups racing on the same code would
// otherwise both succeed.
func (i *Invites) Redeem(_ context.Context, code, user string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	inv, ok := i.byID[strings.TrimSpace(strings.ToLower(code))]
	if !ok {
		return ErrInviteInvalid
	}
	if inv.UsedBy != "" {
		return ErrInviteUsed
	}
	if time.Now().UTC().After(inv.ExpiresAt) {
		return ErrInviteExpired
	}
	inv.UsedBy = user
	inv.UsedAt = time.Now().UTC()
	return nil
}

// Redeemed links the account the code became to the grant behind it, so the
// dashboard does not show a grant with no username against it.
func (i *Invites) Redeemed(ctx context.Context, code, username string) error {
	if i.access == nil {
		return nil
	}
	return i.access.Redeemed(ctx, code, username)
}

func (i *Invites) list() []*Invite {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]*Invite, 0, len(i.byID))
	for _, inv := range i.byID {
		out = append(out, inv)
	}
	return out
}

type inviteError string

func (e inviteError) Error() string { return string(e) }

const (
	ErrInviteInvalid inviteError = "that invite code is not valid"
	ErrInviteUsed    inviteError = "that invite code has already been used"
	ErrInviteExpired inviteError = "that invite code has expired"
)

// ------------------------------------------------------------------ mount

// AccessMount registers the invite and access routes and the dashboard that
// fronts them. Every route is admin-gated by the server's own group, so the
// definition of "administrator" cannot drift between a built-in route and a
// mounted one.
//
// The access store may be nil: the memory driver cannot answer "who had
// access last week", and the routes that need it answer 501 rather than
// existing in a broken state. Invites still work; they just link to nothing.
func AccessMount(inv *Invites, access AccessStore) server.Mount {
	return func(s *server.Server, mux *http.ServeMux) {
		a := &accessAPI{s: s, inv: inv, access: access, log: s.Logger()}
		mux.Handle("POST /v1/admin/invites", s.Admin(a.createInvite))
		mux.Handle("GET /v1/admin/invites", s.Admin(a.listInvites))
		mux.Handle("GET /v1/admin/access", s.Admin(a.listAccess))
		mux.Handle("GET /v1/admin/access/{id}/history", s.Admin(a.accessHistory))
		mux.Handle("POST /v1/admin/access/{id}/revoke", s.Admin(a.revokeAccess))
		mux.HandleFunc("GET /admin", a.serveAdmin)
	}
}

type accessAPI struct {
	s      *server.Server
	inv    *Invites
	access AccessStore
	log    *slog.Logger
}

// createInvite mints one, for an administrator.
func (a *accessAPI) createInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hours int `json:"hours,omitempty"`
		// Who this invite is for. Optional, because an invite minted for
		// somebody standing next to you needs no paperwork — but when it is
		// given, the grant is recorded and the dashboard can account for the
		// account that appears later.
		Email   string `json:"email,omitempty"`
		Name    string `json:"name,omitempty"`
		Company string `json:"company,omitempty"`
		Note    string `json:"note,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	ttl := 48 * time.Hour
	if req.Hours > 0 && req.Hours <= 24*30 {
		ttl = time.Duration(req.Hours) * time.Hour
	}
	by := server.UserOf(r.Context())
	inv := a.inv.Mint(by, ttl)

	// Record the grant, so an invite issued here is visible on the dashboard
	// rather than only becoming an account that appears from nowhere. Without
	// this the dashboard could revoke access it had no record of granting.
	var grantID string
	if a.access != nil && req.Email != "" {
		g, err := a.access.RecordRequest(r.Context(), store.Grant{
			Email: req.Email, Name: req.Name, Company: req.Company,
			UseCase: req.Note, Status: store.StatusRequested,
			ScreenNote: "issued directly by " + by,
		})
		if err != nil {
			a.log.Error("record grant", "err", err)
		} else if err := a.access.GrantAccess(
			r.Context(), g.ID, by, inv.Code, inv.ExpiresAt); err != nil {
			a.log.Error("mark granted", "err", err)
		} else {
			grantID = g.ID
		}
	}

	a.log.Info("invite created", "by", inv.CreatedBy, "expires", inv.ExpiresAt,
		"grant", grantID)
	server.WriteJSON(w, http.StatusCreated, map[string]any{
		"code": inv.Code, "created_by": inv.CreatedBy,
		"created_at": inv.CreatedAt, "expires_at": inv.ExpiresAt,
		"grant_id": grantID,
	})
}

// listInvites shows outstanding invites, for an administrator.
func (a *accessAPI) listInvites(w http.ResponseWriter, r *http.Request) {
	server.WriteJSON(w, http.StatusOK, a.inv.list())
}

// ------------------------------------------------------------------ access

// accessGuard answers 501 when the deployment has no access store, rather
// than pretending. The memory driver cannot answer "who had access last
// week", and a dashboard that silently shows an empty list is worse than one
// that says why it is empty.
func (a *accessAPI) accessGuard(w http.ResponseWriter) bool {
	if a.access == nil {
		server.WriteJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "access records need the postgres storage driver; " +
				"this deployment runs on the memory driver",
		})
		return false
	}
	return true
}

// listAccess returns every grant: requested, granted, revoked and expired.
func (a *accessAPI) listAccess(w http.ResponseWriter, r *http.Request) {
	if !a.accessGuard(w) {
		return
	}
	gs, err := a.access.Grants(r.Context())
	if err != nil {
		a.log.Error("list access", "err", err)
		server.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot read access records"})
		return
	}
	// Counted here rather than in the browser, so the numbers on the dashboard
	// and the numbers in the API cannot disagree.
	sum := map[string]int{"total": len(gs)}
	for _, g := range gs {
		switch {
		case g.Active():
			sum["active"]++
		case g.Status == store.StatusRevoked:
			sum["revoked"]++
		case g.Status == store.StatusRequested:
			sum["pending"]++
		default:
			sum["expired"]++
		}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"grants": gs, "summary": sum})
}

// accessHistory returns the append-only trail behind one grant.
func (a *accessAPI) accessHistory(w http.ResponseWriter, r *http.Request) {
	if !a.accessGuard(w) {
		return
	}
	evs, err := a.access.AccessHistory(r.Context(), r.PathValue("id"))
	if err != nil {
		server.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "no such grant"})
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"events": evs})
}

// revokeAccess ends someone's access, records why, and tells them.
func (a *accessAPI) revokeAccess(w http.ResponseWriter, r *http.Request) {
	if !a.accessGuard(w) {
		return
	}
	var req struct {
		Clause string `json:"clause"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	// A clause is required. "Revoked for a reason nobody wrote down" is how an
	// access log stops being useful, and the clause is what the person is told.
	if !validClause(req.Clause) {
		server.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "clause must be one of P1..P7 — see the access policy",
		})
		return
	}

	by := server.UserOf(r.Context())
	g, err := a.access.Revoke(r.Context(), r.PathValue("id"), by, req.Clause, req.Note)
	if err != nil {
		server.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "no such grant"})
		return
	}

	// Cut the session before anything else can fail. Revocation that leaves a
	// live session working is not revocation.
	if local := a.s.LocalAuth(); g.Username != "" && local != nil {
		n := local.RevokeUser(g.Username)
		a.log.Info("access revoked", "user", g.Username, "by", by,
			"clause", req.Clause, "sessions_ended", n)
	}

	// Then tell them. Best-effort: a bounced notice must not leave the account
	// still working because the handler returned an error.
	go a.mailRevocation(g)

	server.WriteJSON(w, http.StatusOK, g)
}

func validClause(c string) bool {
	switch c {
	case "P1", "P2", "P3", "P4", "P5", "P6", "P7":
		return true
	}
	return false
}

// clauseText is what the person is told. The policy document is the long
// form; this is the sentence that goes in the email, so it has to stand alone.
var clauseText = map[string]string{
	"P1": "attempting to attack the service itself — sandbox escape, privilege escalation, or reaching another account's data",
	"P2": "using the service to attack others",
	"P3": "illegal content or purpose",
	"P4": "automated or resource abuse, including sharing one account across a team",
	"P5": "obtaining access under a false name, employer or purpose",
	"P6": "capacity — this is not about anything you did",
	"P7": "you asked us to close the account",
}

// The notice is sent through Resend. The sender, the reply address and the
// policy link name the operator of the hosted console; the environment
// overrides them for a deployment that is somebody else's.
const (
	mailEndpoint     = "https://api.resend.com/emails"
	mailFromEnv      = "ACCESS_FROM"   // e.g. "Support <support@example.com>"
	mailReplyEnv     = "ACCESS_TO"     // where a reply lands
	policyURLEnv     = "ACCESS_POLICY" // the policy the clauses are quoted from
	defaultMailFrom  = "Zybuu <support@zybuu.com>"
	defaultMailReply = "support@zybuu.com"
	defaultPolicyURL = "https://zybuu.com/abhed/access-policy"
)

// mailRevocation tells someone their access has ended, and why.
//
// Best-effort and asynchronous: the account is already disabled and the
// session already cut by the time this runs. A revocation that failed to send
// mail is still a revocation, and blocking the handler on an SMTP round trip
// would make the security action depend on a third party being up.
func (a *accessAPI) mailRevocation(g store.Grant) {
	key := os.Getenv("RESEND_API_KEY")
	if key == "" || g.Email == "" {
		return
	}
	from := envOr(mailFromEnv, defaultMailFrom)
	reply := envOr(mailReplyEnv, defaultMailReply)

	first := g.Name
	if i := strings.IndexByte(first, ' '); i > 0 {
		first = first[:i]
	}
	if first == "" {
		first = "there"
	}

	why := clauseText[g.RevokedCode]
	if why == "" {
		why = "a policy matter"
	}

	lines := []string{
		"Hi " + first + ",",
		"",
		"Your access to the Abhed console has ended.",
		"",
		"Reason (" + g.RevokedCode + "): " + why + ".",
		"",
		"The account is disabled rather than deleted, so the record of what",
		"happened stays intact. Your transcripts are no longer reachable by you;",
		"they are not destroyed, and you can ask for an export.",
		"",
	}
	lines = append(lines,
		"The policy is at "+envOr(policyURLEnv, defaultPolicyURL)+" — the clause",
		"above is quoted from the version in force today.",
		"",
		"If you think this is wrong, reply. It reaches a person, and we would",
		"rather be wrong briefly than unfair permanently.",
		"",
		"Abhed itself is not affected by this. It runs on your own hardware",
		"under your own rules, and that conversation is still open.",
		"",
		"— Zybuu",
	)

	payload, err := json.Marshal(map[string]any{
		"from": from, "to": []string{g.Email}, "reply_to": reply,
		"subject": "Your Abhed console access has ended",
		"text":    strings.Join(lines, "\n"),
	})
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", mailEndpoint, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.log.Warn("revocation notice not sent", "email", g.Email, "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		a.log.Warn("revocation notice refused", "email", g.Email, "status", resp.StatusCode)
		return
	}
	a.log.Info("revocation notice sent", "email", g.Email, "clause", g.RevokedCode)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
