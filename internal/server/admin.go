package server

import (
	"bytes"
	"context"
	"github.com/yuvrajsingh/titan/internal/store"
	"os"

	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yuvrajsingh/titan/internal/auth"
)

// Administration: the routes that change what the deployment is, rather than
// what one session does.
//
// Every local account was equal until now. That was survivable while the only
// thing a user could do was run their own sessions, and stops being survivable
// the moment the console can add a skill or an MCP server — a skill is
// *instructions*, and an MCP server is a *tool surface*, so letting any
// signed-in user add either means letting them rewrite what the agent does.
//
// The group plumbing already existed end to end: auth.User.Groups is persisted
// by both stores and copied into the session Identity, and auth.RequireGroup
// checks it. What was missing was anything that set the group, and anywhere
// that checked it per-route.

// DefaultAdminGroup is used when the operator names none.
const DefaultAdminGroup = "titan-admin"

func (s *Server) adminGroup() string {
	if g := s.opts.Config.Auth.AdminGroup; g != "" {
		return g
	}
	return DefaultAdminGroup
}

// admin wraps a handler so only members of the admin group reach it.
//
// Registered per-route rather than around the mux. Config.Auth.RequireGroup
// wraps the entire handler, which is the wrong granularity for this: it also
// covers the paths the auth layer deliberately made public, so a deployment
// that sets it locks users out of the sign-in page they need in order to
// acquire the group.
//
// Ordering works out because auth.Middleware.Wrap sits outside the mux: by the
// time a route is dispatched, the identity is already in the context.
func (s *Server) admin(h http.HandlerFunc) http.Handler {
	return auth.RequireGroup(s.adminGroup(), h)
}

// isAdmin reports whether the caller is an administrator, for handlers that
// change their answer rather than refusing outright.
func (s *Server) isAdmin(r *http.Request) bool {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		return false
	}
	for _, g := range id.Groups {
		if g == s.adminGroup() {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- invites

// Signup was disabled outright because Titan runs shell commands, and open
// registration on a public URL hands a stranger an agent with a shell. That is
// still true — but "no signup at all" was blocking adoption, and the answer is
// not to open the door, it is to decide who gets a key.
//
// An invite is single-use and expires. New accounts land with no groups, so an
// invited user is an ordinary user until an administrator promotes them.
type invite struct {
	Code      string    `json:"code"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	UsedBy    string    `json:"used_by,omitempty"`
	UsedAt    time.Time `json:"used_at,omitempty"`
}

// inviteStore keeps invites in memory.
//
// Deliberately not durable: an invite is short-lived by design, and losing the
// outstanding ones on restart is a mild inconvenience, where persisting them
// would put a credential-shaped secret in the store for no gain.
type inviteStore struct {
	mu   sync.Mutex
	byID map[string]*invite
}

func newInviteStore() *inviteStore {
	return &inviteStore{byID: map[string]*invite{}}
}

func (s *inviteStore) mint(by string, ttl time.Duration) *invite {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("server: crypto/rand unavailable: " + err.Error())
	}
	code := strings.ToLower(base32.StdEncoding.WithPadding(
		base32.NoPadding).EncodeToString(b[:]))

	inv := &invite{
		Code: code, CreatedBy: by,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[code] = inv
	return inv
}

// redeem consumes an invite, or reports why it cannot be used.
//
// Single-use is enforced here, under the lock, rather than by the caller
// checking and then using — two signups racing on the same code would
// otherwise both succeed.
func (s *inviteStore) redeem(code, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.byID[strings.TrimSpace(strings.ToLower(code))]
	if !ok {
		return errInviteInvalid
	}
	if inv.UsedBy != "" {
		return errInviteUsed
	}
	if time.Now().UTC().After(inv.ExpiresAt) {
		return errInviteExpired
	}
	inv.UsedBy = user
	inv.UsedAt = time.Now().UTC()
	return nil
}

func (s *inviteStore) list() []*invite {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*invite, 0, len(s.byID))
	for _, i := range s.byID {
		out = append(out, i)
	}
	return out
}

type inviteError string

func (e inviteError) Error() string { return string(e) }

const (
	errInviteInvalid inviteError = "that invite code is not valid"
	errInviteUsed    inviteError = "that invite code has already been used"
	errInviteExpired inviteError = "that invite code has expired"
)

// createInvite mints one, for an administrator.
func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hours int `json:"hours,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	ttl := 48 * time.Hour
	if req.Hours > 0 && req.Hours <= 24*30 {
		ttl = time.Duration(req.Hours) * time.Hour
	}
	inv := s.invites.mint(userOf(r.Context()), ttl)
	s.log.Info("invite created", "by", inv.CreatedBy, "expires", inv.ExpiresAt)
	writeJSON(w, http.StatusCreated, inv)
}

// listInvites shows outstanding invites, for an administrator.
func (s *Server) listInvites(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.invites.list())
}

// ------------------------------------------------------------------ users

type adminUser struct {
	Username string    `json:"username"`
	Email    string    `json:"email,omitempty"`
	Tenant   string    `json:"tenant"`
	Groups   []string  `json:"groups,omitempty"`
	Admin    bool      `json:"admin"`
	Created  time.Time `json:"created_at"`
}

// listUsers reports the accounts on this deployment.
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	local := s.localAuth()
	if local == nil {
		writeError(w, http.StatusNotImplemented,
			"this deployment does not hold its own accounts")
		return
	}
	users, err := local.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list accounts")
		return
	}
	admin := s.adminGroup()
	out := make([]adminUser, 0, len(users))
	for _, u := range users {
		au := adminUser{
			Username: u.Username, Email: u.Email, Tenant: u.Tenant,
			Groups: u.Groups, Created: u.CreatedAt,
		}
		for _, g := range u.Groups {
			if g == admin {
				au.Admin = true
			}
		}
		out = append(out, au)
	}
	writeJSON(w, http.StatusOK, out)
}

// setUserAdmin grants or revokes administrator rights.
func (s *Server) setUserAdmin(w http.ResponseWriter, r *http.Request) {
	local := s.localAuth()
	if local == nil {
		writeError(w, http.StatusNotImplemented,
			"this deployment does not hold its own accounts")
		return
	}
	var req struct {
		Username string `json:"username"`
		Admin    bool   `json:"admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	// Removing your own admin rights would leave a deployment with no way back
	// in if you are the only administrator. Refuse rather than let someone
	// lock themselves out of their own settings.
	if !req.Admin && req.Username == userOf(r.Context()) {
		writeError(w, http.StatusConflict,
			"you cannot remove your own administrator rights")
		return
	}
	if err := local.SetGroups(r.Context(), req.Username,
		s.adminGroup(), req.Admin); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.log.Info("admin rights changed", "user", req.Username,
		"admin", req.Admin, "by", userOf(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------ access

// accessGuard answers 501 when the deployment has no access store, rather
// than pretending. The memory driver cannot answer "who had access last
// week", and a dashboard that silently shows an empty list is worse than one
// that says why it is empty.
func (s *Server) accessGuard(w http.ResponseWriter) bool {
	if s.opts.Access == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "access records need the postgres storage driver; " +
				"this deployment runs on the memory driver",
		})
		return false
	}
	return true
}

// listAccess returns every grant: requested, granted, revoked and expired.
func (s *Server) listAccess(w http.ResponseWriter, r *http.Request) {
	if !s.accessGuard(w) {
		return
	}
	gs, err := s.opts.Access.Grants(r.Context())
	if err != nil {
		s.log.Error("list access", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot read access records"})
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
	writeJSON(w, http.StatusOK, map[string]any{"grants": gs, "summary": sum})
}

// accessHistory returns the append-only trail behind one grant.
func (s *Server) accessHistory(w http.ResponseWriter, r *http.Request) {
	if !s.accessGuard(w) {
		return
	}
	evs, err := s.opts.Access.AccessHistory(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such grant"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs})
}

// revokeAccess ends someone's access, records why, and tells them.
func (s *Server) revokeAccess(w http.ResponseWriter, r *http.Request) {
	if !s.accessGuard(w) {
		return
	}
	var req struct {
		Clause string `json:"clause"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	// A clause is required. "Revoked for a reason nobody wrote down" is how an
	// access log stops being useful, and the clause is what the person is told.
	if !validClause(req.Clause) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "clause must be one of P1..P7 — see docs/access-policy.md",
		})
		return
	}

	by := userOf(r.Context())
	g, err := s.opts.Access.Revoke(r.Context(), r.PathValue("id"), by, req.Clause, req.Note)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such grant"})
		return
	}

	// Cut the session before anything else can fail. Revocation that leaves a
	// live session working is not revocation.
	if g.Username != "" && s.local != nil {
		n := s.local.RevokeUser(g.Username)
		s.log.Info("access revoked", "user", g.Username, "by", by,
			"clause", req.Clause, "sessions_ended", n)
	}

	// Then tell them. Best-effort: a bounced notice must not leave the account
	// still working because the handler returned an error.
	go s.mailRevocation(g)

	writeJSON(w, http.StatusOK, g)
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

// mailRevocation tells someone their access has ended, and why.
//
// Best-effort and asynchronous: the account is already disabled and the
// session already cut by the time this runs. A revocation that failed to send
// mail is still a revocation, and blocking the handler on an SMTP round trip
// would make the security action depend on a third party being up.
func (s *Server) mailRevocation(g store.Grant) {
	key := os.Getenv("RESEND_API_KEY")
	if key == "" || g.Email == "" {
		return
	}
	from := os.Getenv("ACCESS_FROM")
	if from == "" {
		from = "Zybuu <support@zybuu.com>"
	}
	reply := os.Getenv("ACCESS_TO")
	if reply == "" {
		reply = "support@zybuu.com"
	}

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

	body := strings.Join([]string{
		"Hi " + first + ",",
		"",
		"Your access to the Titan console has ended.",
		"",
		"Reason (" + g.RevokedCode + "): " + why + ".",
		"",
		"The account is disabled rather than deleted, so the record of what",
		"happened stays intact. Your transcripts are no longer reachable by you;",
		"they are not destroyed, and you can ask for an export.",
		"",
		"The policy is at https://zybuu.com/titan/access-policy — the clause",
		"above is quoted from the version in force today.",
		"",
		"If you think this is wrong, reply. It reaches a person, and we would",
		"rather be wrong briefly than unfair permanently.",
		"",
		"Titan itself is not affected by this. It runs on your own hardware",
		"under your own rules, and that conversation is still open.",
		"",
		"— Zybuu",
	}, "\n")

	payload, err := json.Marshal(map[string]any{
		"from": from, "to": []string{g.Email}, "reply_to": reply,
		"subject": "Your Titan console access has ended",
		"text":    body,
	})
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.log.Warn("revocation notice not sent", "email", g.Email, "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		s.log.Warn("revocation notice refused", "email", g.Email, "status", resp.StatusCode)
		return
	}
	s.log.Info("revocation notice sent", "email", g.Email, "clause", g.RevokedCode)
}
