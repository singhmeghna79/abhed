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
	"github.com/yuvrajsingh/titan/internal/config"
	"github.com/yuvrajsingh/titan/internal/model"
	"github.com/yuvrajsingh/titan/internal/policy"
	"github.com/yuvrajsingh/titan/internal/tools"
)

type Options struct {
	Addr      string
	Workspace string
	Config    config.Config
	Adapter   model.Adapter
	Registry  *tools.Registry
	Logger    *slog.Logger
}

// Server holds live sessions and serves the API.
type Server struct {
	opts    Options
	store   *agent.MemStore
	log     *slog.Logger
	mu      sync.RWMutex
	running map[string]*liveSession
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
	return &Server{
		opts:    opts,
		store:   agent.NewMemStore(),
		log:     opts.Logger,
		running: make(map[string]*liveSession),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/sessions", s.createSession)
	mux.HandleFunc("GET /v1/sessions", s.listSessions)
	mux.HandleFunc("GET /v1/sessions/{id}/events", s.streamEvents)
	mux.HandleFunc("GET /v1/sessions/{id}/replay", s.replaySession)
	mux.HandleFunc("POST /v1/sessions/{id}/interrupt", s.interruptSession)
	mux.HandleFunc("POST /v1/sessions/{id}/approve", s.approveAction)
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("GET /", s.serveConsole)

	return s.withMiddleware(mux)
}

// withMiddleware applies identity and logging. Authentication is delegated to
// the enterprise IdP in production (docs/ops/air-gap.md §4); this reads the
// identity headers a reverse proxy sets after authenticating.
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		user := r.Header.Get("X-Titan-User")
		if user == "" {
			user = "anonymous"
		}
		tenant := r.Header.Get("X-Titan-Tenant")
		if tenant == "" {
			tenant = "default"
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
	live, ok := s.session(id, tenantOf(r.Context()))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
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
	if backlog, err := s.store.Since(live.ID, lastSeq); err == nil {
		for _, ev := range backlog {
			writeSSE(w, ev)
			lastSeq = ev.Seq
		}
		flusher.Flush()
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
	if _, ok := s.session(id, tenantOf(r.Context())); !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	events, err := s.store.Events(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) interruptSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	live, ok := s.session(id, tenantOf(r.Context()))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
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

func writeSSE(w http.ResponseWriter, ev agent.Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, data)
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
