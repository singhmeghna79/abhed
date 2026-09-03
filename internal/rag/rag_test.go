package rag

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, body string, capture *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			raw, _ := io.ReadAll(r.Body)
			m := map[string]any{}
			json.Unmarshal(raw, &m)
			*capture = m
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
}

// A flat {"results":[{"text":...}]} shape, which most retrieval APIs return.
func TestSearchSimpleShape(t *testing.T) {
	srv := serve(t, `{"results":[
		{"text":"Restart the pod with kubectl rollout restart.","source":"runbook.md","score":0.91},
		{"text":"Check the readiness probe first.","source":"probes.md","score":0.72}]}`, nil)
	defer srv.Close()

	r, err := New(Config{Name: "ops", URL: srv.URL, ResultsPath: "results"})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := r.Search(context.Background(), "pod restart", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d passages, want 2", len(ps))
	}
	if !strings.Contains(ps[0].Text, "rollout restart") {
		t.Errorf("text = %q", ps[0].Text)
	}
	if ps[0].Source != "runbook.md" || ps[0].Score != 0.91 {
		t.Errorf("metadata lost: %+v", ps[0])
	}
}

// Elasticsearch nests results under hits.hits with the body in _source, which
// is exactly the case a per-vendor client would otherwise be needed for.
func TestSearchNestedShape(t *testing.T) {
	srv := serve(t, `{"hits":{"total":2,"hits":[
		{"_score":4.2,"_id":"doc-1","_source":{"body":"Deep agents need a harness.","title":"Design"}}]}}`, nil)
	defer srv.Close()

	r, err := New(Config{
		Name: "docs", URL: srv.URL,
		ResultsPath: "hits.hits",
		TextField:   "_source.body",
		TitleField:  "_source.title",
		ScoreField:  "_score",
		SourceField: "_id",
	})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := r.Search(context.Background(), "harness", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ps) != 1 {
		t.Fatalf("got %d passages, want 1", len(ps))
	}
	if ps[0].Text != "Deep agents need a harness." || ps[0].Title != "Design" ||
		ps[0].Score != 4.2 || ps[0].Source != "doc-1" {
		t.Errorf("nested mapping wrong: %+v", ps[0])
	}
}

// With no mapping at all, common field names should still work: a simple
// endpoint must not require configuration to be usable.
func TestSearchInfersCommonFields(t *testing.T) {
	srv := serve(t, `[{"content":"Inferred without any mapping.","url":"http://x/1"}]`, nil)
	defer srv.Close()

	r, _ := New(Config{Name: "c", URL: srv.URL})
	ps, err := r.Search(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ps) != 1 || ps[0].Text != "Inferred without any mapping." || ps[0].Source != "http://x/1" {
		t.Errorf("inference failed: %+v", ps)
	}
}

func TestSearchBuildsRequestBody(t *testing.T) {
	var got map[string]any
	srv := serve(t, `{"results":[]}`, &got)
	defer srv.Close()

	r, _ := New(Config{
		Name: "c", URL: srv.URL,
		QueryField: "params.question", TopKField: "params.k",
		Body:        map[string]any{"index": "runbooks"},
		ResultsPath: "results",
	})
	r.Search(context.Background(), "how do I restart", 3)

	if got["index"] != "runbooks" {
		t.Errorf("fixed body field lost: %+v", got)
	}
	params, ok := got["params"].(map[string]any)
	if !ok {
		t.Fatalf("dotted query_field did not nest: %+v", got)
	}
	if params["question"] != "how do I restart" {
		t.Errorf("query not at params.question: %+v", params)
	}
	if params["k"] != float64(3) {
		t.Errorf("top_k not at params.k: %+v", params)
	}
}

func TestSearchGETWithQueryParam(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		io.WriteString(w, `{"results":[{"text":"ok"}]}`)
	}))
	defer srv.Close()

	r, _ := New(Config{Name: "c", URL: srv.URL + "/search", Method: "GET",
		QueryParam: "q", TopKField: "n", ResultsPath: "results"})
	if _, err := r.Search(context.Background(), "hello world", 4); err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(gotURL, "q=hello+world") || !strings.Contains(gotURL, "n=4") {
		t.Errorf("GET url = %q", gotURL)
	}
}

// An error must say what to fix. A wrong results_path is the most common
// misconfiguration and produces an empty list, not an obvious failure.
func TestSearchExplainsBadResultsPath(t *testing.T) {
	srv := serve(t, `{"data":{"passages":[{"text":"x"}]}}`, nil)
	defer srv.Close()

	r, _ := New(Config{Name: "c", URL: srv.URL, ResultsPath: "results"})
	_, err := r.Search(context.Background(), "q", 5)
	if err == nil {
		t.Fatal("a wrong results_path silently returned nothing")
	}
	if !strings.Contains(err.Error(), "results_path") {
		t.Errorf("error does not name the setting to fix: %v", err)
	}
}

func TestSearchReportsAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	r, _ := New(Config{Name: "corp", URL: srv.URL})
	_, err := r.Search(context.Background(), "q", 5)
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Errorf("401 not reported as a credentials problem: %v", err)
	}
}

func TestSearchSendsHeaders(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("X-Api-Key")
		io.WriteString(w, `{"results":[]}`)
	}))
	defer srv.Close()

	r, _ := New(Config{Name: "c", URL: srv.URL, ResultsPath: "results",
		Headers: map[string]string{"X-Api-Key": "secret"}})
	r.Search(context.Background(), "q", 5)
	if auth != "secret" {
		t.Errorf("X-Api-Key = %q, want the configured key", auth)
	}
}

func TestNewValidates(t *testing.T) {
	for _, c := range []Config{
		{URL: "http://x"},              // no name
		{Name: "a"},                    // no url
		{Name: "a", URL: "ftp://host"}, // wrong scheme
	} {
		if _, err := New(c); err == nil {
			t.Errorf("accepted invalid config %+v", c)
		}
	}
}

// Empty results are an answer, not a failure: reporting an error invites the
// model to retry the same query.
func TestToolReportsEmptyWithoutError(t *testing.T) {
	srv := serve(t, `{"results":[]}`, nil)
	defer srv.Close()
	r, _ := New(Config{Name: "ops", URL: srv.URL, ResultsPath: "results"})

	res := (&Tool{R: r}).Run(context.Background(), nil, json.RawMessage(`{"query":"x"}`))
	if res.IsError {
		t.Errorf("empty results reported as an error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "No passages") {
		t.Errorf("unhelpful empty message: %s", res.Content)
	}
}

func TestToolNameIsNamespaced(t *testing.T) {
	r, _ := New(Config{Name: "Ops Runbooks", URL: "http://x"})
	if got := (&Tool{R: r}).Name(); got != "rag_ops_runbooks" {
		t.Errorf("Name() = %q, want rag_ops_runbooks", got)
	}
}

func TestToolIsReadOnly(t *testing.T) {
	r, _ := New(Config{Name: "c", URL: "http://x"})
	if (&Tool{R: r}).Mutates() {
		t.Error("a search claims to mutate, so it would prompt for approval")
	}
}
