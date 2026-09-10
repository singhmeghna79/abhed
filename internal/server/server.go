// Package server exposes Titan as a multi-user service.
//
// The CLI and the web console consume the SAME event stream (docs §10): there
// is one agent loop implementation, and the server is a transport over it, not
// a second product. That is what keeps the two surfaces from drifting.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuvrajsingh/titan/internal/agent"
	"github.com/yuvrajsingh/titan/internal/auth"
	"github.com/yuvrajsingh/titan/internal/config"
	"github.com/yuvrajsingh/titan/internal/index"
	"github.com/yuvrajsingh/titan/internal/mcp"
	"github.com/yuvrajsingh/titan/internal/model"
	"github.com/yuvrajsingh/titan/internal/policy"
	"github.com/yuvrajsingh/titan/internal/skills"
	"github.com/yuvrajsingh/titan/internal/store"
	"github.com/yuvrajsingh/titan/internal/tools"
)

// EventStore is what the server needs from a store: durable append plus live
// subscription. Both the in-memory and Postgres stores satisfy it, so server
// code never branches on the backend.
type EventStore interface {
	agent.Store
	Subscribe(sessionID string) <-chan agent.Event
	Unsubscribe(sessionID string, ch <-chan agent.Event)
}

// SessionRecorder is implemented by durable stores that track session rows.
// Optional: the memory store does not, and the server degrades gracefully.
type SessionRecorder interface {
	CreateSession(ctx context.Context, s store.SessionRecord) error
	ListSessions(ctx context.Context, limit int) ([]store.SessionRecord, error)
}

type Options struct {
	Addr      string
	Workspace string
	Config    config.Config
	Adapter   model.Adapter
	Registry  *tools.Registry
	// SkillListing is the rendered skill index for the system prompt. The
	// server takes the rendered string rather than the registry, because the
	// registry's only other use is the tool, which is already in Registry.
	SkillListing string
	// SkillDirs are the loaded skills' directories, granted to every session
	// so a skill can reference the scripts and assets shipped beside it.
	SkillDirs []string
	Logger    *slog.Logger
	// Store defaults to an in-memory store when nil.
	Store EventStore
	// Auth verifies callers. Nil means the mode from Config is used.
	Auth *auth.Middleware
	// SkillRoots are the directories skills are looked FOR in — distinct from
	// SkillDirs, which holds each loaded skill's own directory so its assets
	// can be read. A reload has to scan the roots.
	SkillRoots []string
	// SkillRegistry is the loaded skill set. Held alongside SkillListing so a
	// settings change can re-render the listing rather than being stuck with
	// the string computed at startup.
	SkillRegistry *skills.Registry
	// Gateway holds the MCP connections, so a server can be added at runtime.
	Gateway *mcp.Gateway
	// Index backs the retrieval tool, for a reindex triggered from settings.
	Index        *index.Index
	IndexOptions index.BuildOptions
}

// Server holds live sessions and serves the API.
type Server struct {
	opts     Options
	store    EventStore
	sessions SessionRecorder // nil when the store is not durable
	log      *slog.Logger
	mu       sync.RWMutex
	running  map[string]*liveSession

	// Throttles for the endpoints reachable before authentication succeeds.
	signinLimiter  *limiter
	sessionLimiter *limiter

	// Outstanding signup invitations, minted by an administrator.
	invites *inviteStore

	// state holds what a settings change may replace, behind its own lock.
	// Separate from opts, which stays immutable — mixing "set once" and
	// "changes at runtime" in one struct is how a field ends up read without
	// the lock.
	state *mutable
}

type liveSession struct {
	ID        string
	User      string
	Tenant    string
	Loop      *agent.Loop
	Cancel    context.CancelFunc
	Created   time.Time
	Prompt    string
	State     string // running | waiting_approval | done
	Turns     int    // exchanges in this conversation
	cancel    context.CancelFunc
	approvals chan approvalReply
	pending   *pendingApproval
	mu        sync.Mutex
}

type pendingApproval struct {
	EventID string          `json:"event_id"`
	Tool    string          `json:"tool"`
	Args    json.RawMessage `json:"args"`
	Reason  string          `json:"reason"`
	Scope   string          `json:"scope"`
}

type approvalReply struct {
	Approved bool
	Scope    string
}

func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	st := opts.Store
	if st == nil {
		st = agent.NewMemStore()
	}
	s := &Server{
		opts:    opts,
		store:   st,
		log:     opts.Logger,
		running: make(map[string]*liveSession),
		// Ten sign-in attempts a minute is far beyond what a person typing a
		// password needs, and far below what makes guessing viable.
		signinLimiter:  newLimiter(10, time.Minute),
		sessionLimiter: newLimiter(60, time.Minute),
		invites:        newInviteStore(),
		state: &mutable{
			registry: opts.Registry,
			skills:   opts.SkillRegistry,
			gateway:  opts.Gateway,
			cfg:      opts.Config,
		},
	}
	if rec, ok := st.(SessionRecorder); ok {
		s.sessions = rec
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/sessions", s.createSession)
	mux.HandleFunc("GET /v1/sessions", s.listSessions)
	mux.HandleFunc("GET /v1/sessions/{id}/events", s.streamEvents)
	mux.HandleFunc("GET /v1/sessions/{id}/replay", s.replaySession)
	mux.HandleFunc("POST /v1/sessions/{id}/messages", s.postMessage)
	mux.HandleFunc("POST /v1/sessions/{id}/upload", s.uploadFile)
	// Uploading before a session exists: see uploadFile for why a placeholder
	// session was the wrong answer.
	mux.HandleFunc("POST /v1/uploads", s.uploadFile)
	mux.HandleFunc("DELETE /v1/sessions/{id}", s.deleteSession)

	// Administrative routes, gated per-route on group membership rather than
	// by wrapping the whole mux — see admin.go for why that distinction
	// matters. mux.Handle rather than HandleFunc because each is wrapped.
	mux.Handle("POST /v1/admin/invites", s.admin(s.createInvite))
	mux.Handle("GET /v1/admin/invites", s.admin(s.listInvites))
	mux.Handle("GET /v1/admin/users", s.admin(s.listUsers))
	mux.Handle("POST /v1/admin/users/admin", s.admin(s.setUserAdmin))
	mux.Handle("GET /v1/admin/settings", s.admin(s.getSettings))
	mux.Handle("POST /v1/admin/skills/reload", s.admin(s.reloadSkills))
	mux.Handle("POST /v1/admin/mcp", s.admin(s.addMCP))
	mux.Handle("POST /v1/admin/reindex", s.admin(s.reindex))
	mux.HandleFunc("GET /v1/sessions/{id}/files", s.listDownloads)
	mux.HandleFunc("GET /v1/sessions/{id}/download", s.serveDownload)
	mux.HandleFunc("GET /v1/providers", s.listProviders)
	mux.HandleFunc("POST /v1/sessions/{id}/model", s.setSessionModel)
	mux.HandleFunc("POST /v1/sessions/{id}/interrupt", s.interruptSession)
	mux.HandleFunc("POST /v1/sessions/{id}/approve", s.approveAction)
	mux.HandleFunc("GET /v1/health", s.health)
	// Local accounts and an OIDC provider are not mutually exclusive: a
	// deployment that wants "sign in with a password OR with Google" runs
	// both, so each registers only the routes it owns.
	local := s.localAuth()
	oidc := (*auth.Login)(nil)
	if s.opts.Auth != nil {
		oidc = s.opts.Auth.Login
	}
	switch {
	case local != nil:
		mux.HandleFunc("POST /v1/signin", local.SignIn)
		mux.HandleFunc("POST /v1/password", local.ChangePasswordHandler)
		// Registered whenever local accounts exist. The handler decides
		// admission: open, invite-only, or closed. Registering it
		// conditionally would make "invite-only" impossible without a
		// restart, which is the case that matters most.
		mux.HandleFunc("POST /v1/signup", s.signup)
		if oidc != nil {
			// Both enabled: OIDC keeps its own entry points, and sign-out
			// has to clear whichever session the browser actually holds.
			mux.HandleFunc("GET /login", oidc.Start)
			mux.HandleFunc("GET /auth/callback", oidc.Callback)
			mux.HandleFunc("GET /switch-user", oidc.ForceReauth)
			mux.HandleFunc("GET /logout", s.signOutBoth)
			mux.HandleFunc("GET /v1/whoami", s.whoamiEither)
		} else {
			mux.HandleFunc("GET /logout", local.SignOut)
			mux.HandleFunc("GET /v1/whoami", local.Whoami)
			// There is no external IdP to redirect to; the form is on "/".
			mux.HandleFunc("GET /login", s.redirectHome)
		}
	case oidc != nil:
		mux.HandleFunc("GET /login", oidc.Start)
		mux.HandleFunc("GET /auth/callback", oidc.Callback)
		mux.HandleFunc("GET /logout", oidc.Logout)
		mux.HandleFunc("GET /switch-user", oidc.ForceReauth)
		mux.HandleFunc("GET /v1/whoami", oidc.Whoami)
	default:
		// Sign-in is not configured. These routes still answer, because a 404
		// leaves the console unable to tell "no auth here" from "the server is
		// broken" — and a user who clicks Sign out deserves an explanation
		// rather than a Go 404 page.
		mux.HandleFunc("GET /v1/whoami", s.whoamiDisabled)
		mux.HandleFunc("GET /login", s.authDisabledPage)
		mux.HandleFunc("GET /logout", s.authDisabledPage)
	}
	mux.HandleFunc("GET /", s.serveLanding)
	mux.HandleFunc("GET /console", s.serveConsole)
	mux.HandleFunc("GET /v1/overview", s.overview)

	// Order matters and is easy to get backwards: authentication must run
	// BEFORE the layer that reads the identity, so it wraps closest to the
	// outside. An inverted order silently yields anonymous identities.
	var handler http.Handler = mux
	if s.opts.Config.Auth.RequireGroup != "" {
		handler = auth.RequireGroup(s.opts.Config.Auth.RequireGroup, handler)
	}
	handler = s.withMiddleware(handler) // reads identity, logs

	// Sign-in is throttled OUTSIDE authentication, because an unauthenticated
	// attacker is precisely who this limits: by the time the auth layer has
	// rejected a password, the bcrypt comparison has already been paid for.
	authed := s.authMiddleware().Wrap(handler) // establishes identity
	limited := s.throttle(authed)

	// Origin is checked before anything reads a cookie, and headers are set
	// outermost so they are present on rejections too — an error response is
	// still a response a browser will act on.
	guarded := sameOrigin(s.opts.Config.Server.AllowedOrigins)(limited)
	return securityHeaders(s.bodyLimit(guarded), s.opts.Config.Server.HSTS)
}

// bodyLimit caps every request body before any handler decodes it.
//
// Applied centrally rather than per-handler: there are six JSON decode sites
// today, and the one that gets added next month is the one that would have been
// forgotten. Uploads set their own, larger limit inside uploadFile, so they are
// exempted here rather than being clamped to the JSON size.
func (s *Server) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && !strings.HasSuffix(r.URL.Path, "/upload") &&
			r.URL.Path != "/v1/uploads" {
			capBody(w, r)
		}
		next.ServeHTTP(w, r)
	})
}

// throttle rate-limits the endpoints an anonymous caller can reach.
//
// Only the credential endpoints are limited, not the whole API: a signed-in
// user driving an agent legitimately makes many requests, and throttling those
// would degrade normal use to defend against an attacker who is already past
// the door.
func (s *Server) throttle(next http.Handler) http.Handler {
	trustProxy := s.opts.Config.Auth.Mode == "proxy" || s.opts.Config.Server.TrustProxy
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/signin", "/v1/signup", "/v1/password":
			rateLimit(s.signinLimiter, trustProxy, next).ServeHTTP(w, r)
		case "/v1/sessions":
			// Creating a session starts an agent loop, which is the most
			// expensive thing this server does. It is authenticated, so the
			// limit is generous — it exists to bound a runaway client, not to
			// police normal use.
			if r.Method == http.MethodPost {
				rateLimit(s.sessionLimiter, trustProxy, next).ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// authMiddleware builds the identity layer from config unless one was injected.
func (s *Server) authMiddleware() auth.Middleware {
	if s.opts.Auth != nil {
		return *s.opts.Auth
	}
	// Sign-in itself must be reachable without being signed in, or the only
	// way in is barred by the thing it unlocks.
	mw := auth.Middleware{PublicPaths: []string{
		"/", "/v1/health", "/v1/overview", "/login", "/auth/callback", "/logout",
		"/v1/signin", "/v1/signup", "/v1/whoami"}}
	if s.opts.Config.Auth.Mode == "proxy" {
		mw.TrustHeaders = true
	}
	return mw
}

// withMiddleware applies identity and logging. Authentication is delegated to
// the enterprise IdP in production (docs/ops/air-gap.md §4); this reads the
// identity headers a reverse proxy sets after authenticating.
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Identity comes from the auth layer, which has already verified it.
		user, tenant := "anonymous", "default"
		if id, ok := auth.FromContext(r.Context()); ok {
			user, tenant = id.Subject, id.Tenant
			if id.Email != "" {
				user = id.Email
			}
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxTenant, tenant)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		// A panicking handler would otherwise drop the connection with no
		// status and no audit line — the request simply vanishes from the log,
		// which is the worst possible outcome for something internet-facing.
		// The client is told nothing beyond "internal error": a Go stack trace
		// names packages, paths, and versions.
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic serving request",
					"method", r.Method, "path", r.URL.Path,
					"user", user, "panic", v)
				if rec.status == http.StatusOK && !rec.wrote {
					writeError(rec, http.StatusInternalServerError, "internal error")
				}
			}
		}()

		next.ServeHTTP(rec, r.WithContext(ctx))

		s.log.Info("request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"user", user, "tenant", tenant, "duration", time.Since(start))
	})
}

type ctxKey string

const (
	ctxUser   ctxKey = "user"
	ctxTenant ctxKey = "tenant"
)

func userOf(ctx context.Context) string {
	if v, ok := ctx.Value(ctxUser).(string); ok {
		return v
	}
	return "anonymous"
}

func tenantOf(ctx context.Context) string {
	if v, ok := ctx.Value(ctxTenant).(string); ok {
		return v
	}
	return "default"
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	// wrote tracks whether anything reached the client, so panic recovery can
	// tell "nothing was sent, send a 500" from "a response was already
	// streaming", where a second WriteHeader would only log a superfluous-call
	// warning and corrupt the body.
	wrote bool
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.wrote = true
	r.ResponseWriter.WriteHeader(code)
}

// Flush lets SSE writes reach the client through the wrapper.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type createRequest struct {
	Prompt string `json:"prompt"`
	Mode   string `json:"mode,omitempty"`
	// Provider names one of the CONFIGURED providers. Never a URL or a key —
	// see provider.go for why that distinction is load-bearing.
	Provider string `json:"provider,omitempty"`
}

type createResponse struct {
	SessionID string `json:"session_id"`
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	// The mode is resolved before anything is persisted or started, because a
	// rejected mode must not leave a half-created session behind.
	mode, ok := requestMode(s.opts.Config.Permissions.Mode, req.Mode)
	if !ok {
		writeError(w, http.StatusForbidden,
			"mode may only narrow permissions; a client may request \"plan\" and nothing else")
		return
	}

	// One snapshot for the whole of session creation. Taking it once means a
	// settings change landing mid-request cannot give this session a tool
	// registry from before the change and a skill listing from after it.
	registry, skillReg, _ := s.state.snapshot()

	// Resolved before anything is persisted or started: a session half-created
	// against a provider that does not exist is worse than a clean refusal.
	adapter := s.opts.Adapter
	if req.Provider != "" {
		a, _, err := s.resolveProvider(req.Provider)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		adapter = a
	}

	sessionID := newSessionID()

	// Events reference sessions, so the session row must exist first.
	if s.sessions != nil {
		if err := s.sessions.CreateSession(r.Context(), store.SessionRecord{
			ID: sessionID,
			// Leave Tenant empty so the store applies its own configured
			// tenant. With auth.mode=none every request is "default", which
			// would otherwise collide with a store scoped to a real tenant and
			// fail row-level security on the very first session.
			Tenant:    storeTenant(s.opts.Config, tenantOf(r.Context())),
			User:      userOf(r.Context()),
			Workspace: s.opts.Workspace,
			Model:     adapter.Profile().Name,
			Mode:      mode,
			Prompt:    req.Prompt,
			StartedAt: time.Now().UTC(),
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "persist session: "+err.Error())
			return
		}
	}

	rec := agent.NewRecorder(s.store, sessionID, "")

	sess, err := tools.NewSession(s.opts.Workspace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Extra roots come from the operator's config, applied to every session.
	// A refusal here is a misconfiguration, not a per-request problem: fail
	// the session rather than silently running with a narrower scope than the
	// operator asked for.
	dirs := append([]string{}, s.opts.Config.AdditionalDirs...)
	// A skill's own directory is reachable: its instructions reference files
	// beside them, and denying that read is a dead end for the agent.
	dirs = append(dirs, s.opts.SkillDirs...)
	for _, dir := range dirs {
		if err := sess.AddRoot(dir); err != nil {
			writeError(w, http.StatusInternalServerError,
				"additional_dirs: "+err.Error())
			return
		}
	}

	pol := policy.New(policy.Mode(mode))
	pol.Managed = s.opts.Config.Managed
	_ = pol.AddDeny(s.opts.Config.Permissions.Deny...)
	_ = pol.AddAsk(s.opts.Config.Permissions.Ask...)
	_ = pol.AddAllow(s.opts.Config.Permissions.Allow...)

	live := &liveSession{
		ID: sessionID, User: userOf(r.Context()), Tenant: tenantOf(r.Context()),
		Created: time.Now(), Prompt: req.Prompt, State: "running",
		approvals: make(chan approvalReply, 1),
	}

	cfg := agent.DefaultConfig()
	cfg.SystemPrompt = agent.BuildSystemPrompt(agent.BuildOptions{
		Profile:       "main",
		Workspace:     s.opts.Workspace,
		Model:         adapter.Profile().Name,
		ContextWindow: adapter.Profile().ContextWindow,
		MemoryFiles:   agent.DiscoverMemoryFiles(s.opts.Workspace),
		Skills:        s.skillListing(skillReg),
	})
	cfg.MaxTurns = s.opts.Config.Limits.MaxTurns

	loop := agent.NewLoop(adapter, registry, pol, live, sess, rec, cfg)
	loop.Compactor = agent.NewCompactor(adapter, cfg.CompactAt)
	live.Loop = loop

	ctx, cancel := context.WithCancel(context.Background())
	live.Cancel = cancel
	live.cancel = cancel
	live.Turns = 1

	s.mu.Lock()
	s.running[sessionID] = live
	s.mu.Unlock()

	go func() {
		defer cancel()
		reason, err := loop.Run(ctx, req.Prompt)
		live.mu.Lock()
		live.State = "done"
		live.mu.Unlock()
		if err != nil {
			s.log.Error("session failed", "session", sessionID, "error", err)
			return
		}
		s.log.Info("session ended", "session", sessionID, "reason", reason)
	}()

	writeJSON(w, http.StatusAccepted, createResponse{SessionID: sessionID})
}

type sessionSummary struct {
	ID      string    `json:"id"`
	User    string    `json:"user"`
	Tenant  string    `json:"tenant"`
	Prompt  string    `json:"prompt"`
	State   string    `json:"state"`
	Created time.Time `json:"created"`
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	tenant := tenantOf(r.Context())
	user := userOf(r.Context())

	// A durable store also returns sessions from before this process started,
	// which is what makes audit useful after a restart.
	if s.sessions != nil {
		records, err := s.sessions.ListSessions(r.Context(), 200)
		if err == nil {
			out := make([]sessionSummary, 0, len(records))
			for _, rec := range records {
				// The store hands back every session it holds. Row-level
				// security scopes that by TENANT in Postgres, and not at all
				// in the memory store — neither scopes it by user. So the
				// filter has to be here, or one person's list of prompts
				// (which is a list of what they were working on, and often
				// what they uploaded) is shown to everyone else in the tenant.
				if !ownsSession(rec.Tenant, rec.User, tenant, user) {
					continue
				}
				state := "done"
				if rec.EndedAt == nil {
					state = "running"
				}
				s.mu.RLock()
				if live, found := s.running[rec.ID]; found {
					live.mu.Lock()
					state = live.State
					live.mu.Unlock()
				}
				s.mu.RUnlock()
				out = append(out, sessionSummary{
					ID: rec.ID, User: rec.User, Tenant: rec.Tenant,
					Prompt: rec.Prompt, State: state, Created: rec.StartedAt,
				})
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		s.log.Warn("durable session list failed, falling back to in-process", "error", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]sessionSummary, 0, len(s.running))
	for _, l := range s.running {
		// Tenant isolation is enforced here AND at the data layer: a boundary
		// that exists in only one place is not a boundary (docs/ops §4). The
		// user check is the same rule s.session() applies to a single session,
		// applied to the list — they must agree, or the list advertises
		// sessions that then 404.
		if !ownsSession(l.Tenant, l.User, tenant, user) {
			continue
		}
		l.mu.Lock()
		out = append(out, sessionSummary{
			ID: l.ID, User: l.User, Tenant: l.Tenant,
			Prompt: l.Prompt, State: l.State, Created: l.Created,
		})
		l.mu.Unlock()
	}
	writeJSON(w, http.StatusOK, out)
}

// mayAccess reports whether the request's caller owns session id, checking the
// live map first and then the durable store.
//
// The store is consulted because a session outlives the process that ran it:
// after a restart every session is "not running", and refusing those would make
// history unreadable. When neither source knows the id, access is denied —
// failing closed, so an unknown id can never be mistaken for an owned one.
func (s *Server) mayAccess(r *http.Request, id string) bool {
	tenant, user := tenantOf(r.Context()), userOf(r.Context())
	if _, ok := s.session(id, tenant, user); ok {
		return true
	}
	if s.sessions == nil {
		return false
	}
	records, err := s.sessions.ListSessions(r.Context(), 500)
	if err != nil {
		// A store that cannot answer is not permission to proceed.
		return false
	}
	for _, rec := range records {
		if rec.ID == id {
			return ownsSession(rec.Tenant, rec.User, tenant, user)
		}
	}
	return false
}

// ownsSession reports whether a caller may see a session.
//
// One definition used by both the list and the single-session lookup, because
// the failure this prevents is precisely the two drifting apart: a list that is
// more permissive than the fetch leaks prompts, and one that is stricter hides
// sessions the user can actually open.
//
// The anonymous case is deliberately permissive: with auth disabled every
// caller is "anonymous" in tenant "default", and filtering by user would leave
// a single-user local deployment unable to see its own history.
func ownsSession(recTenant, recUser, tenant, user string) bool {
	if recTenant != tenant {
		return false
	}
	if user == "" || user == "anonymous" {
		return true
	}
	return recUser == user
}

// streamEvents serves the session's event stream over SSE, resumable via
// Last-Event-ID so a dropped connection does not lose the session.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// A session this process is not running may still be replayable from a
	// durable store — that is the whole point of event sourcing. Looking only
	// at the in-memory map meant every session from before a restart returned
	// 404, so clicking one in the UI showed a blank pane.
	live, running := s.session(id, tenantOf(r.Context()), userOf(r.Context()))
	if !running {
		// Not running is not the same as not ours. Ownership is checked
		// against the stored record before any event is streamed, or a
		// finished session becomes readable by anyone who knows its id.
		if !s.mayAccess(r, id) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		backlog, err := s.store.Events(id)
		if err != nil || len(backlog) == 0 {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var lastSeq int64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		lastSeq, _ = strconv.ParseInt(v, 10, 64)
	}

	// Replay what was missed before subscribing, so no event is dropped in the
	// gap between reconnect and subscription.
	if backlog, err := s.store.Since(id, lastSeq); err == nil {
		for _, ev := range backlog {
			writeSSE(w, ev)
			lastSeq = ev.Seq
		}
		flusher.Flush()
	}

	// Only a session still running in this process can produce new events.
	// For a replayed one the backlog above is the whole story, so close cleanly
	// rather than holding a connection open that will never deliver anything.
	if !running {
		return
	}

	events := s.store.Subscribe(live.ID)
	defer s.store.Unsubscribe(live.ID, events)

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-events:
			if !open {
				return
			}
			if ev.Seq <= lastSeq {
				continue
			}
			lastSeq = ev.Seq
			writeSSE(w, ev)
			flusher.Flush()
			if ev.Type == agent.EvSessionEnded {
				return
			}
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// replaySession returns the full event list. Deterministic replay is what makes
// audit and incident reconstruction work (docs P6).
func (s *Server) replaySession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// A durable store can replay a session this process never ran, so a miss in
	// the live map is not by itself a 404. Ownership still has to be proven
	// against the stored record: row-level security scopes the query by tenant,
	// never by user, so without this any signed-in account could replay a
	// colleague's full transcript by id.
	if !s.mayAccess(r, id) {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	events, err := s.store.Events(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(events) == 0 {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// postMessage continues an existing conversation.
//
// This is what makes the console a chat rather than a series of one-shots: the
// Loop is retained per session, so a follow-up resolves against everything that
// came before — including which files have been read, which is what lets the
// second message edit what the first one looked at.
func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	live, ok := s.session(id, tenantOf(r.Context()), userOf(r.Context()))
	if !ok {
		writeError(w, http.StatusNotFound,
			"session not found — it may have ended with this server process")
		return
	}

	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	live.mu.Lock()
	busy := live.State == "running" || live.State == "waiting_approval"
	if !busy {
		live.State = "running"
		live.Turns++
	}
	live.mu.Unlock()

	if busy {
		// A message to a working agent steers it rather than being refused.
		// Interrupting and re-asking throws away everything the run has
		// already established — the files read, the tool results, the context
		// built — and makes the user pay for it twice. The message is applied
		// at the next turn boundary, so a call in flight still completes and
		// the transcript never shows one with no result.
		live.Loop.Steer(req.Prompt)
		writeJSON(w, http.StatusAccepted, map[string]string{
			"session_id": id,
			"delivery":   "steered",
		})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	live.mu.Lock()
	live.cancel = cancel
	live.mu.Unlock()

	go func() {
		defer cancel()
		reason, err := live.Loop.Continue(ctx, req.Prompt)
		live.mu.Lock()
		live.State = "done"
		live.mu.Unlock()
		if err != nil {
			s.log.Error("follow-up failed", "session", id, "error", err)
			return
		}
		s.log.Info("follow-up ended", "session", id, "reason", reason)
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"session_id": id})
}

func (s *Server) interruptSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	live, ok := s.session(id, tenantOf(r.Context()), userOf(r.Context()))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	live.mu.Lock()
	c := live.cancel
	live.mu.Unlock()
	if c != nil {
		c()
	}
	live.Cancel()
	w.WriteHeader(http.StatusNoContent)
}

type approveRequest struct {
	Approved bool   `json:"approved"`
	Scope    string `json:"scope,omitempty"`
}

func (s *Server) approveAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	live, ok := s.session(id, tenantOf(r.Context()), userOf(r.Context()))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	var req approveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	select {
	case live.approvals <- approvalReply{Approved: req.Approved, Scope: req.Scope}:
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusConflict, "no approval is pending for this session")
	}
}

// localAuth returns the local-account handler, or nil when this deployment
// does not hold passwords itself.
func (s *Server) localAuth() *auth.LocalAuth {
	if s.opts.Auth == nil {
		return nil
	}
	return s.opts.Auth.Local
}

// redirectHome sends /login to the front door, which is where the sign-in form
// lives when Titan holds the accounts.
func (s *Server) redirectHome(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusFound)
}

// signOutBoth clears whichever session the browser is holding. With both auth
// modes enabled we cannot know which one signed this user in, and clearing the
// wrong one leaves them still signed in after clicking Sign out.
func (s *Server) signOutBoth(w http.ResponseWriter, r *http.Request) {
	if local := s.localAuth(); local != nil {
		if _, found := local.FromCookie(r); found {
			local.SignOut(w, r)
			return
		}
	}
	s.opts.Auth.Login.Logout(w, r)
}

// whoamiEither answers for whichever session exists.
func (s *Server) whoamiEither(w http.ResponseWriter, r *http.Request) {
	if local := s.localAuth(); local != nil {
		if _, found := local.FromCookie(r); found {
			local.Whoami(w, r)
			return
		}
	}
	s.opts.Auth.Login.Whoami(w, r)
}

// signup creates an account from the sign-in page. Off unless a deployment
// explicitly opts in: on an internal tool, open registration is a way in for
// anyone who can reach the port, not a convenience.
func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	local := s.localAuth()
	if local == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "self-registration is disabled"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Invite   string `json:"invite,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid request"})
		return
	}
	// Three admission modes. Open registration on a public URL hands a
	// stranger an agent with a shell, so it stays off by default — but
	// "closed" was blocking adoption, and an invite is the middle ground:
	// the operator decides who gets in without having to create every
	// account by hand.
	//
	// A new account gets NO groups, so an invited user is an ordinary user
	// until an administrator promotes them.
	if !s.opts.Config.Auth.AllowSignup {
		if strings.TrimSpace(req.Invite) == "" {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "an invite code is required to register here"})
			return
		}
		if err := s.invites.redeem(req.Invite, req.Username); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": err.Error()})
			return
		}
	}

	u := auth.User{
		Username: req.Username, Email: req.Email, Name: req.Name,
		Tenant: orDefaultStr(s.opts.Config.Auth.DefaultTenant, "default"),
	}
	if err := local.CreateUser(r.Context(), u, req.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// whoamiDisabled reports that this deployment runs without authentication.
// The console uses it to decide whether to show a user chip at all.
func (s *Server) whoamiDisabled(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": false,
		"auth_mode":     orDefaultStr(s.opts.Config.Auth.Mode, "none"),
		"reason":        "authentication is not configured on this server",
	})
}

// authDisabledPage explains why /login and /logout do nothing here, and how to
// turn them on. A bare 404 reads as a bug; this reads as a setting.
func (s *Server) authDisabledPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(authDisabledHTML))
}

// serveLanding is the front door.
//
// Previously / went straight into the workspace, which told a first-time
// visitor nothing about what Titan is and gave a configured deployment no
// place to sign in. It now shows what this instance actually is — model,
// sandbox tier, storage, auth mode, live session counts — and routes on:
// straight through when there is nothing to sign in to, or to the IdP when
// there is.
func (s *Server) serveLanding(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	w.Write([]byte(landingHTML))
}

// overviewResponse is what the landing page renders. Everything here is a fact
// about THIS deployment, so the page describes the instance in front of you
// rather than a product in the abstract.
type overviewResponse struct {
	Model         string `json:"model"`
	ContextWindow int    `json:"context_window"`
	// Workspace is empty for anonymous callers: it is an absolute host path,
	// so it names the operator's account and directory layout.
	Workspace  string `json:"workspace,omitempty"`
	Sandbox    string `json:"sandbox"`
	SandboxNet bool   `json:"sandbox_network"`
	// Isolation describes the boundary in the terms that actually apply to this
	// deployment. A containerised Titan reports sandbox tier "none" — correct,
	// because the boundary is the container around the whole process rather
	// than a sandbox inside it — and presenting that bare number as a warning
	// would tell the reader the opposite of the truth.
	Isolation string `json:"isolation,omitempty"`
	// IsolationOK is whether the deployment is actually contained, as opposed
	// to whether a particular tier string was configured.
	IsolationOK bool `json:"isolation_ok"`
	Storage       string `json:"storage"`
	Durable       bool   `json:"durable"`
	AuthMode      string `json:"auth_mode"`
	SignInURL     string `json:"sign_in_url,omitempty"`
	Authenticated bool   `json:"authenticated"`
	// LocalAuth tells the landing page to render a username/password form
	// rather than a redirect button.
	LocalAuth     bool     `json:"local_auth"`
	AllowSignup   bool     `json:"allow_signup"`
	// InviteSignup reports that registration is possible with a code.
	//
	// Distinct from AllowSignup, which means "anyone may register". The two
	// booleans together are what let the page offer a code field instead of
	// either an open form or nothing at all — with only AllowSignup, an
	// invite-only deployment is indistinguishable from a closed one and the
	// UI correctly hides a door that is in fact open.
	InviteSignup bool `json:"invite_signup"`
	ProviderLabel string   `json:"provider_label,omitempty"`
	User          string   `json:"user,omitempty"`
	Tenant        string   `json:"tenant,omitempty"`
	WebSearch     string   `json:"web_search"`
	Retrieval     bool     `json:"retrieval"`
	MCPServers    int      `json:"mcp_servers"`
	Tools         []string `json:"tools"`
	Sessions      int      `json:"sessions"`
	Events        int64    `json:"events"`
	Running       int      `json:"running"`
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	cfg := s.opts.Config
	o := overviewResponse{
		Model:         s.opts.Adapter.Profile().Name,
		ContextWindow: s.opts.Adapter.Profile().ContextWindow,
		AuthMode:      orDefaultStr(cfg.Auth.Mode, "none"),
		Durable:       cfg.Storage.Driver == "postgres",
		Retrieval:     cfg.Retrieval.Enabled,
		MCPServers:    len(cfg.MCP.Servers),
	}

	o.Storage = "in-memory (sessions end with this process)"
	if o.Durable {
		o.Storage = "postgres · sessions survive restart"
	}

	o.Sandbox = orDefaultStr(cfg.Sandbox.MinTier, "process")
	o.SandboxNet = cfg.Sandbox.AllowNetwork

	// TITAN_IN_CONTAINER is set by the deployment image, so this reports how
	// the process is actually running rather than what a config file claims.
	// The distinction matters: inside a container, tier "none" is the correct
	// setting and the strongest available posture, because the boundary is the
	// container itself.
	if os.Getenv("TITAN_IN_CONTAINER") != "" {
		o.Isolation = "container"
		o.IsolationOK = true
	} else {
		o.Isolation = o.Sandbox
		o.IsolationOK = o.Sandbox != "none"
	}

	o.WebSearch = "disabled"
	if cfg.WebSearch.Enabled {
		o.WebSearch = orDefaultStr(cfg.WebSearch.Provider, "duckduckgo")
	}

	if reg := s.state.toolRegistry(); reg != nil {
		o.Tools = reg.Names()
	}

	// Sign-in only matters when there is somewhere to sign in TO.
	if local := s.localAuth(); local != nil {
		o.LocalAuth = true
		o.AllowSignup = cfg.Auth.AllowSignup
		o.InviteSignup = !cfg.Auth.AllowSignup
		if id, found := local.FromCookie(r); found {
			o.Authenticated = true
			o.User = orDefaultStr(id.Email, id.Subject)
			o.Tenant = id.Tenant
		}
	}
	if s.opts.Auth != nil && s.opts.Auth.Login != nil {
		o.SignInURL = "/login?return=%2Fconsole"
		o.ProviderLabel = auth.ProviderLabel(cfg.Auth.Provider, cfg.Auth.Issuer)
		if id, found := s.opts.Auth.Login.FromCookie(r); found {
			o.Authenticated = true
			o.User = orDefaultStr(id.Email, id.Subject)
			o.Tenant = id.Tenant
		}
	}

	// The workspace is an absolute path on the host: it names the operator's
	// account and directory layout, which is reconnaissance for anyone probing
	// the box. This endpoint is public so the landing page can describe the
	// deployment honestly before sign-in, and everything above is a property of
	// the deployment rather than of the machine. The path is not, so it is
	// disclosed only to someone who has already authenticated.
	if o.Authenticated {
		o.Workspace = s.opts.Workspace
	}

	s.mu.RLock()
	for _, l := range s.running {
		l.mu.Lock()
		if l.State == "running" || l.State == "waiting_approval" {
			o.Running++
		}
		l.mu.Unlock()
	}
	s.mu.RUnlock()

	if s.sessions != nil {
		if recs, err := s.sessions.ListSessions(r.Context(), 500); err == nil {
			o.Sessions = len(recs)
		}
	}
	if pg, ok := s.store.(interface {
		Stats(context.Context) (int64, int64, error)
	}); ok {
		if _, events, err := pg.Stats(r.Context()); err == nil {
			o.Events = events
		}
	}

	writeJSON(w, http.StatusOK, o)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	n := len(s.running)
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"sessions": n,
		"model":    s.opts.Adapter.Profile().Name,
	})
}

// session resolves a live session for a caller, or reports absence.
//
// Both the tenant AND the user must match. Tenancy alone was the original
// check, which quietly meant every user in a tenant could read another user's
// transcript, post to their agent, interrupt it, and — worst of all — answer
// its approval prompts. Approving a dangerous tool call on someone else's
// behalf is a privilege the model was never meant to accept from a bystander.
//
// A caller who is not the owner gets the same "not found" as a caller who
// invented the ID, so the lookup does not confirm that a session exists.
func (s *Server) session(id, tenant, user string) (*liveSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	live, found := s.running[id]
	if !found || !ownsSession(live.Tenant, live.User, tenant, user) {
		return nil, false
	}
	return live, true
}

// Approve implements agent.Approver for a server session: it publishes the
// pending request and blocks until a reviewer answers or the session is
// cancelled. This is what enables headless runs with a human gate.
func (l *liveSession) Approve(ctx context.Context, tool string, args json.RawMessage, res policy.Result) (bool, error) {
	l.mu.Lock()
	l.State = "waiting_approval"
	l.pending = &pendingApproval{Tool: tool, Args: args, Reason: res.Reason, Scope: res.Scope}
	l.mu.Unlock()

	defer func() {
		l.mu.Lock()
		l.State = "running"
		l.pending = nil
		l.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case reply := <-l.approvals:
		return reply.Approved, nil
	case <-time.After(30 * time.Minute):
		// Fail closed: an unanswered approval must not become an approval.
		return false, nil
	}
}

// storeTenant reconciles the request's tenant with the store's configured one.
// When authentication is off there is no meaningful per-request tenant, so the
// configured one wins; with real auth the token's tenant is authoritative.
func storeTenant(cfg config.Config, requestTenant string) string {
	if cfg.Auth.Mode == "none" || cfg.Auth.Mode == "" {
		if cfg.Storage.Tenant != "" {
			return cfg.Storage.Tenant
		}
	}
	return requestTenant
}

func orDefaultStr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func writeSSE(w http.ResponseWriter, ev agent.Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	// No "event:" field, deliberately. Naming an SSE event makes the browser
	// dispatch it to addEventListener(name), and EventSource.onmessage then
	// never fires — which silently produced an empty transcript in the console
	// while curl, which ignores the field, showed the data arriving fine.
	// The type is already in the JSON payload, so one generic handler is both
	// correct and simpler than registering a listener per event type.
	fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.Seq, data)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.opts.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// A slow body is the other half of slowloris: headers arrive promptly,
		// then the body trickles in a byte at a time and holds the connection
		// open. Generous enough for a large upload on a poor connection.
		ReadTimeout: 5 * time.Minute,
		// Idle keep-alive connections cost a goroutine each; an attacker opening
		// thousands and sending nothing is otherwise free.
		IdleTimeout: 2 * time.Minute,
		// 1 MB of headers is the Go default and far more than anything here
		// sends; stating it makes the bound deliberate rather than inherited.
		MaxHeaderBytes: 1 << 20,
		// No write timeout: SSE streams are long-lived by design.
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	s.log.Info("titan server listening", "addr", s.opts.Addr, "workspace", s.opts.Workspace)
	return srv.ListenAndServe()
}
