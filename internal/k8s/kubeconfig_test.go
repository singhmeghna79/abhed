package k8s

import "testing"

// kubectl writes sequence items at the SAME indent as their key. The first
// parser required deeper indentation and reported every real kubeconfig as
// having zero clusters, with no error at all.
const sameIndentConfig = `apiVersion: v1
clusters:
- cluster:
    server: https://a.example:6443
    insecure-skip-tls-verify: true
  name: cluster-a
- cluster:
    server: https://b.example:6443
  name: cluster-b
contexts:
- context:
    cluster: cluster-a
    user: user-a
    namespace: prod
  name: ctx-a
current-context: ctx-a
users:
- name: user-a
  user:
    token: abc123
`

func TestParseSameIndentSequences(t *testing.T) {
	kc, err := parseKubeconfig([]byte(sameIndentConfig))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(kc.Clusters) != 2 {
		t.Fatalf("clusters = %d, want 2 — same-indent sequences were dropped", len(kc.Clusters))
	}
	if kc.Clusters[0].Server != "https://a.example:6443" {
		t.Errorf("cluster server = %q", kc.Clusters[0].Server)
	}
	if !kc.Clusters[0].InsecureSkipTLSVerify {
		t.Error("insecure-skip-tls-verify not read")
	}
	if kc.Clusters[1].Name != "cluster-b" {
		t.Errorf("second cluster name = %q", kc.Clusters[1].Name)
	}
	if len(kc.Contexts) != 1 || kc.Contexts[0].Namespace != "prod" {
		t.Errorf("contexts = %+v", kc.Contexts)
	}
	if kc.CurrentContext != "ctx-a" {
		t.Errorf("current-context = %q", kc.CurrentContext)
	}
	if len(kc.Users) != 1 || kc.Users[0].Token != "abc123" {
		t.Errorf("users = %+v", kc.Users)
	}
}

// Nested sequences also appear, and the reader must not swallow the following
// top-level key.
const indentedConfig = `clusters:
  - cluster:
      server: https://x:6443
    name: x
users:
  - name: u
    user:
      exec:
        command: aws
        args:
          - eks
          - get-token
`

func TestParseIndentedSequences(t *testing.T) {
	kc, err := parseKubeconfig([]byte(indentedConfig))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(kc.Clusters) != 1 || kc.Clusters[0].Name != "x" {
		t.Fatalf("clusters = %+v", kc.Clusters)
	}
	if len(kc.Users) != 1 {
		t.Fatalf("users = %d, want 1 — the clusters block swallowed them", len(kc.Users))
	}
	ex := kc.Users[0].Exec
	if ex == nil || ex.Command != "aws" {
		t.Fatalf("exec = %+v", ex)
	}
	if len(ex.Args) != 2 || ex.Args[0] != "eks" {
		t.Errorf("exec args = %v", ex.Args)
	}
}

// Silently misreading a CA certificate is worse than refusing the file.
func TestParseRejectsUnsupportedYAML(t *testing.T) {
	for _, src := range []string{
		"a: 1\n---\nb: 2\n",
		"base: &x {a: 1}\nother:\n  <<: *x\n",
	} {
		if _, err := parseKubeconfig([]byte(src)); err == nil {
			t.Errorf("accepted YAML it cannot parse correctly: %q", src)
		}
	}
}

func TestParseQuotedValues(t *testing.T) {
	kc, err := parseKubeconfig([]byte("current-context: \"a: b\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if kc.CurrentContext != "a: b" {
		t.Errorf("current-context = %q, want %q", kc.CurrentContext, "a: b")
	}
}
