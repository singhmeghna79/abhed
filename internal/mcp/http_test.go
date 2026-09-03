package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// modernServer implements the Streamable HTTP shape: one endpoint, POST only,
// replies as plain JSON.
func modernServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// The modern shape refuses the legacy stream probe.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "sess-123")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,%s}`, req.ID, resultFor(req.Method))
	}))
}

// legacyServer implements HTTP+SSE: a long-lived GET carrying replies, and a
// separate POST endpoint announced by an "endpoint" event.
func legacyServer(t *testing.T) *httptest.Server {
	t.Helper()
	replies := make(chan string, 16)
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "event: endpoint\ndata: /messages?session=abc\n\n")
		w.(http.Flusher).Flush()
		for {
			select {
			case msg := <-replies:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
	mux.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusAccepted)
		if req.Method != "notifications/initialized" {
			replies <- fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,%s}`, req.ID, resultFor(req.Method))
		}
	})
	return httptest.NewServer(mux)
}

func resultFor(method string) string {
	switch method {
	case "initialize":
		return `"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"test","version":"1"},"capabilities":{"tools":{}}}`
	case "tools/list":
		return `"result":{"tools":[{"name":"search","description":"Search the corpus","inputSchema":{"type":"object","properties":{"q":{"type":"string"}}}}]}`
	case "tools/call":
		return `"result":{"content":[{"type":"text","text":"three results"}]}`
	}
	return `"result":{}`
}

func TestHTTPTransportModernShape(t *testing.T) {
	srv := modernServer(t)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tr, err := NewHTTPTransport(ctx, HTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	client := NewClient("remote", tr)
	defer client.Close()

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if len(client.tools) != 1 || client.tools[0].Name != "search" {
		t.Fatalf("tools = %+v, want one named search", client.tools)
	}
}

// The older shape is still widely deployed; a transport that only spoke the
// current spec would fail against half the servers in the wild.
func TestHTTPTransportLegacySSEShape(t *testing.T) {
	srv := legacyServer(t)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tr, err := NewHTTPTransport(ctx, HTTPConfig{URL: srv.URL + "/sse"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	client := NewClient("remote", tr)
	defer client.Close()

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if len(client.tools) != 1 || client.tools[0].Name != "search" {
		t.Fatalf("tools = %+v, want one named search", client.tools)
	}
	// And the POST endpoint must have been learned from the endpoint event,
	// not assumed to be the stream URL.
	tr.mu.Lock()
	post := tr.postURL
	tr.mu.Unlock()
	if !strings.Contains(post, "/messages") {
		t.Errorf("postURL = %q, want the endpoint the server announced", post)
	}
}

func TestHTTPTransportSendsHeaders(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		got = r.Header.Get("Authorization")
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,%s}`, req.ID, resultFor(req.Method))
	}))
	defer srv.Close()

	ctx := context.Background()
	tr, err := NewHTTPTransport(ctx, HTTPConfig{
		URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer tok"}})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient("remote", tr)
	defer client.Close()
	client.Initialize(ctx)

	if got != "Bearer tok" {
		t.Errorf("Authorization = %q, want the configured bearer token", got)
	}
}

func TestHTTPTransportRejectsBadURL(t *testing.T) {
	for _, url := range []string{"", "ftp://x", "not-a-url"} {
		if _, err := NewHTTPTransport(context.Background(), HTTPConfig{URL: url}); err == nil {
			t.Errorf("accepted %q as an MCP url", url)
		}
	}
}

func TestResolveEndpoint(t *testing.T) {
	for _, tc := range []struct{ base, value, want string }{
		{"http://h:1/sse", "/messages?s=1", "http://h:1/messages?s=1"},
		{"http://h:1/sse", "messages", "http://h:1/messages"},
		{"http://h:1/sse", "https://other/m", "https://other/m"},
	} {
		if got := resolveEndpoint(tc.base, tc.value); got != tc.want {
			t.Errorf("resolveEndpoint(%q,%q) = %q, want %q", tc.base, tc.value, got, tc.want)
		}
	}
}
