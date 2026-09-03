package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAPIServer stands in for a cluster, recording what was asked of it.
func fakeAPIServer(t *testing.T, seen *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.RequestURI()+" auth="+r.Header.Get("Authorization"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/pods"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"kind":"PodList","items":[
			  {"metadata":{"name":"api-0","namespace":"prod"},
			   "status":{"phase":"Running","containerStatuses":[{"ready":true,"restartCount":0}]}}]}`)
		case strings.Contains(r.URL.Path, "/log"):
			fmt.Fprint(w, "line one\nline two\n")
		case r.Method == http.MethodDelete:
			fmt.Fprint(w, `{"kind":"Status","status":"Success"}`)
		case r.Method == http.MethodPatch:
			fmt.Fprintf(w, `{"kind":"Deployment","metadata":{"name":"web"},"contentType":%q}`,
				r.Header.Get("Content-Type"))
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"kind":"Status","message":"the server could not find the requested resource"}`)
		}
	}))
}

// writeKubeconfig points a config at the fake server.
func writeKubeconfig(t *testing.T, server string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	body := fmt.Sprintf(`apiVersion: v1
clusters:
- cluster:
    server: %s
    insecure-skip-tls-verify: true
  name: c
contexts:
- context:
    cluster: c
    user: u
    namespace: prod
  name: ctx
current-context: ctx
users:
- name: u
  user:
    token: tok-123
`, server)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGetPodsAgainstFakeCluster(t *testing.T) {
	var seen []string
	srv := fakeAPIServer(t, &seen)
	defer srv.Close()

	mgr := NewManager(Config{Kubeconfig: writeKubeconfig(t, srv.URL)})
	args, _ := json.Marshal(map[string]string{"resource": "pods"})
	res := GetTool{M: mgr}.Run(context.Background(), nil, args)

	if res.IsError {
		t.Fatalf("get pods failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "prod/api-0") || !strings.Contains(res.Content, "Running") {
		t.Errorf("unexpected rendering: %s", res.Content)
	}
	// The namespace must come from the context, and the token must be sent.
	if len(seen) != 1 || !strings.Contains(seen[0], "/api/v1/namespaces/prod/pods") {
		t.Errorf("wrong request: %v", seen)
	}
	if !strings.Contains(seen[0], "auth=Bearer tok-123") {
		t.Errorf("bearer token not sent: %v", seen)
	}
}

func TestGetLogs(t *testing.T) {
	var seen []string
	srv := fakeAPIServer(t, &seen)
	defer srv.Close()

	mgr := NewManager(Config{Kubeconfig: writeKubeconfig(t, srv.URL)})
	args, _ := json.Marshal(map[string]any{
		"resource": "logs", "name": "api-0", "tail": 50, "container": "app"})
	res := GetTool{M: mgr}.Run(context.Background(), nil, args)

	if res.IsError || !strings.Contains(res.Content, "line one") {
		t.Fatalf("logs: %s", res.Content)
	}
	if !strings.Contains(seen[0], "tailLines=50") || !strings.Contains(seen[0], "container=app") {
		t.Errorf("log parameters not passed: %v", seen)
	}
}

// A 404 from the API must arrive as the server's own message, which says
// which resource was missing.
func TestNotFoundCarriesTheAPIMessage(t *testing.T) {
	var seen []string
	srv := fakeAPIServer(t, &seen)
	defer srv.Close()

	mgr := NewManager(Config{Kubeconfig: writeKubeconfig(t, srv.URL)})
	args, _ := json.Marshal(map[string]string{"resource": "services"})
	res := GetTool{M: mgr}.Run(context.Background(), nil, args)

	if !res.IsError {
		t.Fatal("a 404 was reported as success")
	}
	if !strings.Contains(res.Content, "could not find") {
		t.Errorf("API message lost: %s", res.Content)
	}
}

// RBAC denials must be distinguishable from every other failure, because the
// fix is different: ask for permission, not retry.
func TestForbiddenIsNamed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"kind":"Status","message":"pods is forbidden: User cannot list resource"}`)
	}))
	defer srv.Close()

	mgr := NewManager(Config{Kubeconfig: writeKubeconfig(t, srv.URL)})
	args, _ := json.Marshal(map[string]string{"resource": "pods"})
	res := GetTool{M: mgr}.Run(context.Background(), nil, args)

	if !strings.Contains(res.Content, "RBAC") || !strings.Contains(res.Content, "cannot list") {
		t.Errorf("forbidden not surfaced usefully: %s", res.Content)
	}
}

func TestScaleSendsMergePatch(t *testing.T) {
	var seen []string
	srv := fakeAPIServer(t, &seen)
	defer srv.Close()

	mgr := NewManager(Config{Kubeconfig: writeKubeconfig(t, srv.URL)})
	args, _ := json.Marshal(map[string]any{
		"action": "scale", "resource": "deployments", "name": "web", "replicas": 3})
	res := ApplyTool{M: mgr}.Run(context.Background(), nil, args)

	if res.IsError {
		t.Fatalf("scale: %s", res.Content)
	}
	if !strings.Contains(seen[0], "PATCH") || !strings.Contains(seen[0], "/deployments/web/scale") {
		t.Errorf("wrong scale request: %v", seen)
	}
	if !strings.Contains(res.Content, "3 replicas") {
		t.Errorf("unclear confirmation: %s", res.Content)
	}
}

func TestDeleteHitsTheRightPath(t *testing.T) {
	var seen []string
	srv := fakeAPIServer(t, &seen)
	defer srv.Close()

	mgr := NewManager(Config{Kubeconfig: writeKubeconfig(t, srv.URL)})
	args, _ := json.Marshal(map[string]string{
		"action": "delete", "resource": "pods", "name": "api-0"})
	res := ApplyTool{M: mgr}.Run(context.Background(), nil, args)

	if res.IsError {
		t.Fatalf("delete: %s", res.Content)
	}
	if !strings.HasPrefix(seen[0], "DELETE /api/v1/namespaces/prod/pods/api-0") {
		t.Errorf("wrong delete request: %v", seen)
	}
}

// Naming a context that does not exist must list the ones that do.
func TestUnknownContextListsAvailable(t *testing.T) {
	path := writeKubeconfig(t, "https://unused")
	_, err := Open(Config{Kubeconfig: path, Context: "nope"})
	if err == nil {
		t.Fatal("accepted an unknown context")
	}
	if !strings.Contains(err.Error(), "ctx") {
		t.Errorf("error does not list available contexts: %v", err)
	}
}
