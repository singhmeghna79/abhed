package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Local accounts: username and password held by Titan itself.
//
// OIDC is the right answer for an organisation that already has an identity
// provider, and it stays the recommended mode. But it delegates, and a
// deployment with no IdP — a pilot, an air-gapped enclave, a single team
// standing this up before central IT is involved — was left with no way to
// have users at all. That is a real gap, not a configuration preference.
//
// Passwords are bcrypt-hashed. The cost is deliberately left at the library
// default rather than tuned down: sign-in happens once per session, so a few
// hundred milliseconds is invisible to a person and expensive to an attacker
// with the hash file.

var (
	ErrBadCredentials = errors.New("incorrect username or password")
	ErrUserExists     = errors.New("that username is already taken")
	ErrWeakPassword   = errors.New("password must be at least 10 characters")
	ErrNoSuchUser     = errors.New("no such user")
)

// User is a local account.
type User struct {
	Username  string    `json:"username"`
	Email     string    `json:"email,omitempty"`
	Name      string    `json:"name,omitempty"`
	Tenant    string    `json:"tenant"`
	Groups    []string  `json:"groups,omitempty"`
	Hash      string    `json:"-"` // never serialized
	CreatedAt time.Time `json:"created_at"`
	// MustChange forces a password change at next sign-in, used for the
	// bootstrap admin so a generated password cannot become permanent.
	MustChange bool `json:"must_change_password,omitempty"`
}

// UserStore persists local accounts.
type UserStore interface {
	Get(ctx context.Context, username string) (*User, error)
	Put(ctx context.Context, u *User) error
	List(ctx context.Context) ([]*User, error)
	Delete(ctx context.Context, username string) error
}

// MemoryUserStore keeps accounts in memory. Adequate for a single-process
// pilot; a durable deployment should use the Postgres store so accounts
// survive a restart.
type MemoryUserStore struct {
	mu    sync.RWMutex
	users map[string]*User
}

func NewMemoryUserStore() *MemoryUserStore {
	return &MemoryUserStore{users: map[string]*User{}}
}

func (m *MemoryUserStore) Get(_ context.Context, username string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, found := m.users[strings.ToLower(username)]
	if !found {
		return nil, ErrNoSuchUser
	}
	copy := *u
	return &copy, nil
}

func (m *MemoryUserStore) Put(_ context.Context, u *User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy := *u
	m.users[strings.ToLower(u.Username)] = &copy
	return nil
}

func (m *MemoryUserStore) List(_ context.Context) ([]*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*User, 0, len(m.users))
	for _, u := range m.users {
		copy := *u
		out = append(out, &copy)
	}
	return out, nil
}

func (m *MemoryUserStore) Delete(_ context.Context, username string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.users, strings.ToLower(username))
	return nil
}

// LocalAuth handles username/password sign-in.
type LocalAuth struct {
	Store      UserStore
	CookieName string
	SessionTTL time.Duration
	Secure     bool

	mu       sync.RWMutex
	sessions map[string]*browserSession
}

func NewLocalAuth(store UserStore, ttl time.Duration, secure bool) *LocalAuth {
	if ttl == 0 {
		ttl = 12 * time.Hour
	}
	l := &LocalAuth{
		Store: store, CookieName: "titan_session",
		SessionTTL: ttl, Secure: secure,
		sessions: map[string]*browserSession{},
	}
	go l.reap()
	return l
}

func (l *LocalAuth) reap() {
	for range time.Tick(10 * time.Minute) {
		now := time.Now()
		l.mu.Lock()
		for k, s := range l.sessions {
			if now.After(s.Expires) {
				delete(l.sessions, k)
			}
		}
		l.mu.Unlock()
	}
}

var validUsername = regexp.MustCompile(`^[a-zA-Z0-9._-]{2,64}$`)

// CreateUser adds an account.
func (l *LocalAuth) CreateUser(ctx context.Context, u User, password string) error {
	if !validUsername.MatchString(u.Username) {
		return fmt.Errorf("username must be 2-64 characters of letters, digits, dot, dash or underscore")
	}
	// Ten characters is a deliberate floor: short enough that people will not
	// write it down, long enough that bcrypt's cost actually matters.
	if len(password) < 10 {
		return ErrWeakPassword
	}
	if existing, _ := l.Store.Get(ctx, u.Username); existing != nil {
		return ErrUserExists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	u.Hash = string(hash)
	u.Username = strings.ToLower(u.Username)
	if u.Tenant == "" {
		u.Tenant = "default"
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	return l.Store.Put(ctx, &u)
}

// Authenticate verifies credentials.
func (l *LocalAuth) Authenticate(ctx context.Context, username, password string) (*User, error) {
	u, err := l.Store.Get(ctx, username)
	if err != nil {
		// Hash anyway so a missing user takes the same time as a wrong
		// password: otherwise response timing enumerates valid usernames.
		bcrypt.CompareHashAndPassword(
			[]byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"),
			[]byte(password))
		return nil, ErrBadCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(password)); err != nil {
		return nil, ErrBadCredentials
	}
	return u, nil
}

// ChangePassword updates a user's password after verifying the current one.
func (l *LocalAuth) ChangePassword(ctx context.Context, username, current, next string) error {
	u, err := l.Authenticate(ctx, username, current)
	if err != nil {
		return err
	}
	if len(next) < 10 {
		return ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.Hash = string(hash)
	u.MustChange = false
	return l.Store.Put(ctx, u)
}

// issue creates a browser session for a signed-in user.
func (l *LocalAuth) issue(w http.ResponseWriter, u *User) {
	sid := randomToken()
	l.mu.Lock()
	l.sessions[sid] = &browserSession{
		Identity: &Identity{
			Subject: u.Username, Email: u.Email, Name: u.Name,
			Tenant: u.Tenant, Groups: u.Groups,
			Expires: time.Now().Add(l.SessionTTL).Unix(),
		},
		Expires: time.Now().Add(l.SessionTTL),
	}
	l.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name: l.CookieName, Value: sid, Path: "/",
		HttpOnly: true, Secure: l.Secure, SameSite: http.SameSiteLaxMode,
		Expires: time.Now().Add(l.SessionTTL),
	})
}

// FromCookie resolves a session cookie to an identity.
func (l *LocalAuth) FromCookie(r *http.Request) (*Identity, bool) {
	c, err := r.Cookie(l.CookieName)
	if err != nil {
		return nil, false
	}
	l.mu.RLock()
	s, found := l.sessions[c.Value]
	l.mu.RUnlock()
	if !found || time.Now().After(s.Expires) {
		return nil, false
	}
	return s.Identity, true
}

type signInRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// SignIn handles a credential POST from the sign-in form.
func (l *LocalAuth) SignIn(w http.ResponseWriter, r *http.Request) {
	var req signInRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	u, err := l.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		// One message for both wrong-user and wrong-password: distinguishing
		// them tells an attacker which usernames exist.
		writeAuthJSON(w, http.StatusUnauthorized,
			map[string]string{"error": ErrBadCredentials.Error()})
		return
	}

	l.issue(w, u)
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true, "username": u.Username, "tenant": u.Tenant,
		"must_change_password": u.MustChange,
	})
}

// SignOut clears the session.
func (l *LocalAuth) SignOut(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(l.CookieName); err == nil {
		l.mu.Lock()
		delete(l.sessions, c.Value)
		l.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name: l.CookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: l.Secure, MaxAge: -1,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

type changePasswordRequest struct {
	Current string `json:"current_password"`
	Next    string `json:"new_password"`
}

// ChangePasswordHandler lets a signed-in user rotate their own password.
func (l *LocalAuth) ChangePasswordHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := l.FromCookie(r)
	if !ok {
		writeAuthJSON(w, http.StatusUnauthorized, map[string]string{"error": "not signed in"})
		return
	}
	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := l.ChangePassword(r.Context(), id.Subject, req.Current, req.Next); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Whoami reports the signed-in user.
func (l *LocalAuth) Whoami(w http.ResponseWriter, r *http.Request) {
	id, ok := l.FromCookie(r)
	if !ok {
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"authenticated": false, "auth_mode": "local"})
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"authenticated": true, "auth_mode": "local",
		"subject": id.Subject, "email": id.Email, "name": id.Name,
		"tenant": id.Tenant, "groups": id.Groups,
	})
}

func writeAuthJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// CreateUserOrReset sets a password without knowing the current one. This is
// the administrative reset path — a user who has forgotten their password
// cannot use ChangePassword, which requires it.
func (l *LocalAuth) CreateUserOrReset(ctx context.Context, u *User, password string) error {
	if len(password) < 10 {
		return ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.Hash = string(hash)
	// An administratively reset password is temporary by definition.
	u.MustChange = true
	return l.Store.Put(ctx, u)
}
