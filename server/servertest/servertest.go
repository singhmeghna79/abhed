// Package servertest builds servers for tests outside the server package.
//
// The server's own tests reach unexported fields; a test in another package —
// an edition exercising the routes it mounts — cannot, and must not need to.
// What every such test needs is the same: a server with a model that answers
// instantly and a tool registry with something in it. This is that, and
// nothing that would let a test depend on how the server is put together.
package servertest

import (
	"context"
	"testing"

	"github.com/zybuu-ai/abhed/config"
	"github.com/zybuu-ai/abhed/internal/model"
	"github.com/zybuu-ai/abhed/server"
	"github.com/zybuu-ai/abhed/internal/tools"
)

// StubAdapter answers every completion with "done" and no tool calls, so a
// session started in a test finishes on its own.
type StubAdapter struct{}

func (StubAdapter) Name() string { return "stub" }
func (StubAdapter) Profile() model.Profile {
	return model.Profile{Name: "stub", ContextWindow: 32000}
}
func (StubAdapter) CountTokens(model.Request) (int, error) { return 10, nil }
func (StubAdapter) Complete(context.Context, model.Request) (<-chan model.Chunk, error) {
	ch := make(chan model.Chunk, 2)
	ch <- model.Chunk{Type: model.ChunkText, Text: "done"}
	ch <- model.Chunk{Type: model.ChunkDone, Usage: &model.Usage{InputTokens: 10}}
	close(ch)
	return ch, nil
}

// Options are the defaults every test server starts from: a temporary
// workspace, the default config, the stub model and two read-only tools.
// Callers adjust them before New.
func Options(t testing.TB) server.Options {
	t.Helper()
	return server.Options{
		Workspace: t.TempDir(),
		Config:    config.Default(),
		Adapter:   StubAdapter{},
		Registry:  tools.NewRegistry(tools.Read{}, tools.Glob{}),
	}
}

// New builds a server with authentication off, after letting each adjust
// function change the options.
func New(t testing.TB, adjust ...func(*server.Options)) *server.Server {
	t.Helper()
	o := Options(t)
	for _, f := range adjust {
		f(&o)
	}
	return server.New(o)
}

// Proxy builds a server that trusts X-Abhed-* headers, the deployment shape
// where a trusted reverse proxy has already authenticated the caller. It is
// the easiest way for a test to be somebody: set X-Abhed-User and, for an
// administrator, X-Abhed-Groups.
func Proxy(t testing.TB, adjust ...func(*server.Options)) *server.Server {
	t.Helper()
	return New(t, append([]func(*server.Options){func(o *server.Options) {
		o.Config.Auth.Mode = "proxy"
	}}, adjust...)...)
}
