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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuvrajsingh/titan/internal/agent"
	"github.com/yuvrajsingh/titan/internal/auth"
	"github.com/yuvrajsingh/titan/internal/config"
	"github.com/yuvrajsingh/titan/internal/model"
	"github.com/yuvrajsingh/titan/internal/policy"
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
	Logger    *slog.Logger
	// Store defaults to an in-memory store when nil.
	Store EventStore
	// Auth verifies callers. Nil means the mode from Config is used.
	Auth *auth.Middleware
}

// Server holds live sessions and serves the API.
type Server struct {
	opts     Options
	store    EventStore
	sessions SessionRecorder // nil when the store is not durable
	log      *slog.Logger
	mu       sync.RWMutex
	running  map[string]*liveSession
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
	mux.HandleFunc("POST /v1/sessions/{id}/interrupt", s.interruptSession)
	mux.HandleFunc("POST /v1/sessions/{id}/approve", s.approveAction)
	mux.HandleFunc("GET /v1/health", s.health)
	if s.opts.Auth != nil && s.opts.Auth.Login != nil {
		lg := s.opts.Auth.Login
		mux.HandleFunc("GET /login", lg.Start)
		mux.HandleFunc("GET /auth/callback", lg.Callback)
		mux.HandleFunc("GET /logout", lg.Logout)
		mux.HandleFunc("GET /switch-user", lg.ForceReauth)
		mux.HandleFunc("GET /v1/whoami", lg.Whoami)
	} else {
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
	handler = s.withMiddleware(handler)     // reads identity, logs
	return s.authMiddleware().Wrap(handler) // establishes identity
}

// authMiddleware builds the identity layer from config unless one was injected.
func (s *Server) authMiddleware() auth.Middleware {
	if s.opts.Auth != nil {
		return *s.opts.Auth
	}
	mw := auth.Middleware{PublicPaths: []string{
		"/", "/v1/health", "/v1/overview", "/login", "/auth/callback", "/logout"}}
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
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
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

	sessionID := "s-" + strconv.FormatInt(time.Now().UnixNano(), 36)

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
			Model:     s.opts.Adapter.Profile().Name,
			Mode:      orDefaultStr(req.Mode, s.opts.Config.Permissions.Mode),
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

	mode := s.opts.Config.Permissions.Mode
	if req.Mode != "" {
		mode = req.Mode
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
		Model:         s.opts.Adapter.Profile().Name,
		ContextWindow: s.opts.Adapter.Profile().ContextWindow,
		MemoryFiles:   agent.DiscoverMemoryFiles(s.opts.Workspace),
	})
	cfg.MaxTurns = s.opts.Config.Limits.MaxTurns

	loop := agent.NewLoop(s.opts.Adapter, s.opts.Registry, pol, live, sess, rec, cfg)
	loop.Compactor = agent.NewCompactor(s.opts.Adapter, cfg.CompactAt)
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

	// A durable store also returns sessions from before this process started,
	// which is what makes audit useful after a restart.
	if s.sessions != nil {
		records, err := s.sessions.ListSessions(r.Context(), 200)
		if err == nil {
			out := make([]sessionSummary, 0, len(records))
			for _, rec := range records {
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
		// that exists in only one place is not a boundary (docs/ops §4).
		if l.Tenant != tenant {
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

// streamEvents serves the session's event stream over SSE, resumable via
// Last-Event-ID so a dropped connection does not lose the session.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// A session this process is not running may still be replayable from a
	// durable store — that is the whole point of event sourcing. Looking only
	// at the in-memory map meant every session from before a restart returned
	// 404, so clicking one in the UI showed a blank pane.
	live, running := s.session(id, tenantOf(r.Context()))
	if !running {
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
	// A durable store can replay a session this process never ran. RLS scopes
	// the query to the caller's tenant, so a cross-tenant id returns nothing.
	if _, ok := s.session(id, tenantOf(r.Context())); !ok && s.sessions == nil {
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
	live, ok := s.session(id, tenantOf(r.Context()))
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
		writeError(w, http.StatusConflict,
			"this session is still working — interrupt it before sending another message")
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
	live, ok := s.session(id, tenantOf(r.Context()))
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
	live, ok := s.session(id, tenantOf(r.Context()))
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
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'")
	w.Write([]byte(landingHTML))
}

// overviewResponse is what the landing page renders. Everything here is a fact
// about THIS deployment, so the page describes the instance in front of you
// rather than a product in the abstract.
type overviewResponse struct {
	Model         string   `json:"model"`
	ContextWindow int      `json:"context_window"`
	Workspace     string   `json:"workspace"`
	Sandbox       string   `json:"sandbox"`
	SandboxNet    bool     `json:"sandbox_network"`
	Storage       string   `json:"storage"`
	Durable       bool     `json:"durable"`
	AuthMode      string   `json:"auth_mode"`
	SignInURL     string   `json:"sign_in_url,omitempty"`
	Authenticated bool     `json:"authenticated"`
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
		Workspace:     s.opts.Workspace,
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

	o.WebSearch = "disabled"
	if cfg.WebSearch.Enabled {
		o.WebSearch = orDefaultStr(cfg.WebSearch.Provider, "duckduckgo")
	}

	if s.opts.Registry != nil {
		o.Tools = s.opts.Registry.Names()
	}

	// Sign-in only matters when there is somewhere to sign in TO.
	if s.opts.Auth != nil && s.opts.Auth.Login != nil {
		o.SignInURL = "/login?return=%2Fconsole"
		if id, ok := s.opts.Auth.Login.FromCookie(r); ok {
			o.Authenticated = true
			o.User = orDefaultStr(id.Email, id.Subject)
			o.Tenant = id.Tenant
		}
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

func (s *Server) session(id, tenant string) (*liveSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	live, found := s.running[id]
	if !found || live.Tenant != tenant {
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
