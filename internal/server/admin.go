package server

import (
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
