package k8s

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestResourcePathRouting(t *testing.T) {
	c := &Cluster{Namespace: "default"}
	for _, tc := range []struct{ resource, ns, name, want string }{
		{"pods", "", "", "/api/v1/namespaces/default/pods"},
		{"pods", "kube-system", "coredns", "/api/v1/namespaces/kube-system/pods/coredns"},
		{"deployments", "prod", "", "/apis/apps/v1/namespaces/prod/deployments"},
		{"nodes", "", "", "/api/v1/nodes"}, // cluster-scoped
		{"namespaces", "ignored", "", "/api/v1/namespaces"},
		{"pods", "*", "", "/api/v1/pods"},                 // all namespaces
		{"po", "", "", "/api/v1/namespaces/default/pods"}, // short form
		{"deploy", "x", "", "/apis/apps/v1/namespaces/x/deployments"},
		{"ingresses", "x", "", "/apis/networking.k8s.io/v1/namespaces/x/ingresses"},
	} {
		got, err := resourcePath(c, tc.resource, tc.ns, tc.name)
		if err != nil {
			t.Errorf("resourcePath(%q,%q,%q): %v", tc.resource, tc.ns, tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resourcePath(%q,%q,%q) = %q, want %q",
				tc.resource, tc.ns, tc.name, got, tc.want)
		}
	}
}

// An unknown resource must name the alternatives, or the model retries the
// same call with different casing.
func TestResourcePathExplainsUnknown(t *testing.T) {
	_, err := resourcePath(&Cluster{Namespace: "d"}, "widgets", "", "")
	if err == nil {
		t.Fatal("accepted an unknown resource")
	}
	if !strings.Contains(err.Error(), "pods") || !strings.Contains(err.Error(), "kubectl") {
		t.Errorf("error does not offer a way forward: %v", err)
	}
}

// The read tool must be non-mutating, or it prompts for every list.
func TestGetToolIsReadOnly(t *testing.T) {
	if (GetTool{}).Mutates() {
		t.Error("k8s_get claims to mutate; every list would need approval")
	}
}

// The write tool must be mutating in every mode. A cluster delete is not
// recoverable the way a file edit is.
func TestApplyToolAlwaysMutates(t *testing.T) {
	if !(ApplyTool{}).Mutates() {
		t.Error("k8s_apply does not declare itself mutating, so it could be auto-approved")
	}
}

func TestRenderListIsCompact(t *testing.T) {
	body := []byte(`{"kind":"PodList","items":[
	  {"metadata":{"name":"web-1","namespace":"prod","managedFields":[{"x":1}]},
	   "status":{"phase":"Running","containerStatuses":[{"ready":true,"restartCount":3}]}},
	  {"metadata":{"name":"web-2","namespace":"prod"},"status":{"phase":"Pending",
	   "containerStatuses":[{"ready":false,"restartCount":0}]}}]}`)
	got := render("pods", body)

	for _, want := range []string{"prod/web-1", "Running", "1/1 ready", "3 restarts", "Pending"} {
		if !strings.Contains(got, want) {
			t.Errorf("render() missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "managedFields") {
		t.Error("render() leaked managedFields, which is pure token cost")
	}
}

func TestRenderEmptyList(t *testing.T) {
	got := render("pods", []byte(`{"kind":"PodList","items":[]}`))
	if !strings.Contains(got, "No pods") {
		t.Errorf("empty list rendered as %q", got)
	}
}

// A single object still has to lose the noise fields.
func TestSummarizeStripsNoise(t *testing.T) {
	got := summarize([]byte(`{"metadata":{"name":"x","managedFields":[1],"resourceVersion":"9","uid":"u"}}`))
	for _, noise := range []string{"managedFields", "resourceVersion", "uid"} {
		if strings.Contains(got, noise) {
			t.Errorf("summarize kept %s", noise)
		}
	}
	if !strings.Contains(got, `"name": "x"`) {
		t.Errorf("summarize dropped the name: %s", got)
	}
}

func TestApplyRejectsIncompleteManifest(t *testing.T) {
	tool := ApplyTool{M: NewManager(Config{})}
	for _, manifest := range []string{
		`{"kind":"Pod"}`,                   // no apiVersion
		`{"apiVersion":"v1"}`,              // no kind
		`{"apiVersion":"v1","kind":"Pod"}`, // no name
		`not json`,
	} {
		args, _ := json.Marshal(map[string]string{"action": "apply", "manifest": manifest})
		res := tool.Run(context.TODO(), nil, args)
		if !res.IsError {
			t.Errorf("accepted an incomplete manifest: %s", manifest)
		}
	}
}

func TestPluralFor(t *testing.T) {
	for _, tc := range []struct{ kind, want string }{
		{"Pod", "pods"},
		{"Deployment", "deployments"},
		{"Ingress", "ingresses"},
		{"NetworkPolicy", "networkpolicies"},
	} {
		if got := pluralFor(tc.kind); got != tc.want {
			t.Errorf("pluralFor(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}
