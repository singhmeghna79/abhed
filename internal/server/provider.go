package server

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/yuvrajsingh/abhed/internal/config"
	"github.com/yuvrajsingh/abhed/internal/model"
)

// Choosing the model from the console.
//
// Twenty providers ship, hosted and on-prem alike, behind one abstraction —
// and switching between them meant editing a config file and restarting. That
// hid the thing Abhed is actually built on: the harness is the product, and the
// model is a swappable input. Being able to run the same task against four
// models and watch the harness hold steady is the argument, made visible.
//
// The rule that matters: a client names a provider from the CONFIGURED set. It
// never supplies a URL, a key, or a model string. Accepting those would let a
// session point the agent at an attacker-controlled endpoint — every prompt,
// every file the agent had read, delivered to a chosen host — and would move
// credentials from the server's environment into a request body.

// providerInfo is one selectable model, as the console sees it.
type providerInfo struct {
	Name          string `json:"name"`
	Model         string `json:"model"`
	Type          string `json:"type"`
	ContextWindow int    `json:"context_window,omitempty"`
	Default       bool   `json:"default"`
}

// providers lists what this deployment can switch between.
func (s *Server) providers() []providerInfo {
	cfg := s.opts.Config
	out := make([]providerInfo, 0, len(cfg.Model.Providers))
	for name, p := range cfg.Model.Providers {
		out = append(out, providerInfo{
			Name: name, Model: p.Model, Type: p.Type,
			ContextWindow: p.ContextWindow,
			Default:       name == cfg.Model.Default,
		})
	}
	// Stable order, or the dropdown reshuffles on every poll.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// listProviders answers the console's model picker.
func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.providers())
}

// resolveProvider turns a client-supplied NAME into an adapter.
//
// The lookup is against the configured map, so an unknown name is refused
// rather than treated as an endpoint. config.ProviderConfig.Adapter() resolves
// APIKeyEnv from the server's environment, which is what keeps the credential
// server-side.
func (s *Server) resolveProvider(name string) (model.Adapter, config.ProviderConfig, error) {
	p, err := s.opts.Config.ProviderNamed(name)
	if err != nil {
		return nil, config.ProviderConfig{}, errUnknownProvider
	}
	a, err := p.Adapter()
	if err != nil {
		return nil, p, err
	}
	return a, p, nil
}

type providerError string

func (e providerError) Error() string { return string(e) }

const errUnknownProvider providerError = "no such provider is configured"

// setSessionModel swaps the model on a running session.
func (s *Server) setSessionModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	live, ok := s.session(id, tenantOf(r.Context()), userOf(r.Context()))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	var req struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	adapter, _, err := s.resolveProvider(req.Provider)
	if err != nil {
		// Named separately from a 404 on the session: "that provider is not
		// configured" is a different fix from "that session is not yours".
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Refused while a turn is in flight. Loop.Adapter is a plain field and the
	// loop goroutine may be reading it, so swapping under a running turn is a
	// data race with a model call on the other end of it. Between turns is the
	// only safe moment, and it is also the only moment a user would want it.
	live.mu.Lock()
	busy := live.State == "running" || live.State == "waiting_approval"
	if busy {
		live.mu.Unlock()
		writeError(w, http.StatusConflict,
			"the session is mid-turn; interrupt it or wait for the turn to finish")
		return
	}
	loop := live.Loop
	live.mu.Unlock()

	if loop == nil {
		writeError(w, http.StatusConflict, "this session has no live loop")
		return
	}
	loop.SetAdapter(adapter)

	s.log.Info("session model changed", "session", id,
		"provider", req.Provider, "user", userOf(r.Context()))
	writeJSON(w, http.StatusOK, map[string]string{
		"provider": req.Provider,
		"model":    adapter.Profile().Name,
	})
}
