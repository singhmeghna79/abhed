package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/yuvrajsingh/titan/internal/tools"
)

// Tool gives the agent access to a Kubernetes cluster.
//
// Two things shape the design.
//
// First, reads and writes are different tools, not one tool with a verb
// argument. Titan's permission engine decides by tool name and arguments, and
// a single k8s tool would force it to parse an opaque verb to tell "list pods"
// from "delete namespace". Splitting them means the read tool is genuinely
// non-mutating and never prompts, while every write goes through approval by
// construction rather than by correctly interpreting a string.
//
// Second, the cluster is chosen by naming a context the operator already has
// in their kubeconfig. The model cannot supply a server URL or a token, so the
// worst it can do is act on a cluster the person running Titan can already
// reach — which is the same blast radius as their own kubectl.

// Manager holds connections, opened lazily and reused.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	clusters map[string]*Cluster
	// sessions holds credentials supplied at runtime by k8s_login, keyed by
	// server URL. They live in memory for the life of the process and are
	// never written anywhere: a token pasted into a chat should not end up in
	// a config file, an event, or a log.
	sessions map[string]sessionCred
}

type sessionCred struct {
	token  string
	server string
}

func NewManager(cfg Config) *Manager {
	return &Manager{cfg: cfg, clusters: map[string]*Cluster{},
		sessions: map[string]sessionCred{}}
}

// login records a credential for this process only, replacing whatever the
// kubeconfig held.
func (m *Manager) login(server, token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[server] = sessionCred{token: token, server: server}
	// Drop cached clients so the next call picks the new credential up rather
	// than reusing a connection built with the expired one.
	m.clusters = map[string]*Cluster{}
}

func (m *Manager) cluster(ctxName string) (*Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clusters[ctxName]; ok {
		return c, nil
	}
	cfg := m.cfg
	if ctxName != "" {
		cfg.Context = ctxName
	}
	// A runtime login for an explicit server bypasses the kubeconfig entirely:
	// there may not be a context for that cluster at all.
	if len(m.sessions) == 1 && ctxName == "" {
		for _, cred := range m.sessions {
			c, err := OpenDirect(cred.server, cred.token, cfg.Namespace)
			if err != nil {
				return nil, err
			}
			m.clusters[ctxName] = c
			return c, nil
		}
	}
	c, err := Open(cfg)
	if err != nil {
		return nil, err
	}
	// Read the map directly: m.mu is already held, and sessionFor would
	// re-lock it. This deadlocked the first time.
	if cred, ok := m.sessions[c.Server]; ok {
		c.bearer = cred.token
	}
	m.clusters[ctxName] = c
	return c, nil
}

// ---------------------------------------------------------------- read tool

type GetTool struct{ M *Manager }

func (GetTool) Name() string  { return "k8s_get" }
func (GetTool) Mutates() bool { return false }

func (GetTool) Description() string {
	return "Read from a Kubernetes cluster: list or describe resources, and fetch pod logs. " +
		"Read-only — it cannot create, change or delete anything. " +
		"Use resource plural names as kubectl does (pods, deployments, services, nodes, events)."
}

func (GetTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "resource":{"type":"string","description":"Resource type, plural: pods, deployments, services, nodes, namespaces, events, configmaps. Use 'logs' to fetch pod logs."},
    "name":{"type":"string","description":"A single resource name. Omit to list all of that type."},
    "namespace":{"type":"string","description":"Namespace. Omit for the context's default; use '*' for all namespaces."},
    "context":{"type":"string","description":"Kubeconfig context naming the cluster. Omit for the current context."},
    "selector":{"type":"string","description":"Label selector, e.g. app=web."},
    "container":{"type":"string","description":"For logs: which container in the pod."},
    "tail":{"type":"integer","description":"For logs: how many trailing lines. Default 200."}
  },
  "required":["resource"]
}`)
}

type getArgs struct {
	Resource  string `json:"resource"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Context   string `json:"context"`
	Selector  string `json:"selector"`
	Container string `json:"container"`
	Tail      int    `json:"tail"`
}

func (t GetTool) Run(ctx context.Context, _ *tools.Session, raw json.RawMessage) tools.Result {
	var a getArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errf("Invalid arguments for k8s_get: %v", err)
	}
	if strings.TrimSpace(a.Resource) == "" {
		return errf("resource is required (pods, deployments, nodes, logs, …)")
	}
	c, err := t.M.cluster(a.Context)
	if err != nil {
		return errf("%v", err)
	}

	if a.Resource == "logs" {
		return t.logs(ctx, c, a)
	}

	path, err := resourcePath(c, a.Resource, a.Namespace, a.Name)
	if err != nil {
		return errf("%v", err)
	}
	if a.Selector != "" {
		path += "?labelSelector=" + urlEscape(a.Selector)
	}

	data, err := c.Do(ctx, "GET", path, nil)
	if err != nil {
		return errf("%v", err)
	}
	return tools.Result{Content: render(a.Resource, data)}
}

func (t GetTool) logs(ctx context.Context, c *Cluster, a getArgs) tools.Result {
	if a.Name == "" {
		return errf("logs needs the pod name in `name`.")
	}
	ns := a.Namespace
	if ns == "" || ns == "*" {
		ns = c.Namespace
	}
	tail := a.Tail
	if tail <= 0 {
		tail = 200
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/log?tailLines=%d",
		ns, a.Name, tail)
	if a.Container != "" {
		path += "&container=" + urlEscape(a.Container)
	}
	data, err := c.Do(ctx, "GET", path, nil)
	if err != nil {
		return errf("%v", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return tools.Result{Content: fmt.Sprintf("Pod %s/%s produced no log output.", ns, a.Name)}
	}
	return tools.Result{Content: string(data)}
}

// ---------------------------------------------------------------- write tool

type ApplyTool struct{ M *Manager }

func (ApplyTool) Name() string { return "k8s_apply" }

// Mutates is true, which is what routes every call through approval. This is
// the whole safety story for cluster writes: there is no mode in which it is
// auto-approved, because a mistaken delete in a production namespace is not
// recoverable by /undo the way a file edit is.
func (ApplyTool) Mutates() bool { return true }

func (ApplyTool) Description() string {
	return "Change a Kubernetes cluster: apply a manifest, scale a workload, delete a resource, " +
		"or restart a rollout. Every call requires human approval. " +
		"Prefer k8s_get first to confirm what you are about to change."
}

func (ApplyTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "action":{"type":"string","enum":["apply","delete","scale","restart"],"description":"What to do."},
    "manifest":{"type":"string","description":"For apply: the resource as JSON or YAML-free JSON."},
    "resource":{"type":"string","description":"For delete/scale/restart: resource type, plural."},
    "name":{"type":"string","description":"For delete/scale/restart: the resource name."},
    "namespace":{"type":"string","description":"Namespace. Omit for the context's default."},
    "context":{"type":"string","description":"Kubeconfig context naming the cluster."},
    "replicas":{"type":"integer","description":"For scale: the desired replica count."}
  },
  "required":["action"]
}`)
}

type applyArgs struct {
	Action    string `json:"action"`
	Manifest  string `json:"manifest"`
	Resource  string `json:"resource"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Context   string `json:"context"`
	Replicas  *int   `json:"replicas"`
}

func (t ApplyTool) Run(ctx context.Context, _ *tools.Session, raw json.RawMessage) tools.Result {
	var a applyArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errf("Invalid arguments for k8s_apply: %v", err)
	}
	c, err := t.M.cluster(a.Context)
	if err != nil {
		return errf("%v", err)
	}

	switch a.Action {
	case "apply":
		return t.apply(ctx, c, a)
	case "delete":
		return t.delete(ctx, c, a)
	case "scale":
		return t.scale(ctx, c, a)
	case "restart":
		return t.restart(ctx, c, a)
	}
	return errf("unknown action %q (want apply, delete, scale or restart)", a.Action)
}

func (t ApplyTool) apply(ctx context.Context, c *Cluster, a applyArgs) tools.Result {
	if strings.TrimSpace(a.Manifest) == "" {
		return errf("apply needs a manifest.")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(a.Manifest), &obj); err != nil {
		return errf("manifest must be JSON: %v. Convert YAML to JSON first.", err)
	}
	kind, _ := obj["kind"].(string)
	apiVersion, _ := obj["apiVersion"].(string)
	if kind == "" || apiVersion == "" {
		return errf("manifest needs both apiVersion and kind.")
	}
	meta, _ := obj["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	if name == "" {
		return errf("manifest metadata.name is required.")
	}
	ns := a.Namespace
	if ns == "" {
		if v, ok := meta["namespace"].(string); ok {
			ns = v
		} else {
			ns = c.Namespace
		}
	}

	base := apiBase(apiVersion) + "/" + pluralFor(kind)
	if isNamespaced(kind) {
		base = apiBase(apiVersion) + "/namespaces/" + ns + "/" + pluralFor(kind)
	}

	// Server-side apply: one PATCH that creates or updates, so there is no
	// read-modify-write race between checking existence and writing.
	path := base + "/" + name + "?fieldManager=titan&force=true"
	body, _ := json.Marshal(obj)
	data, err := c.doPatch(ctx, path, body, "application/apply-patch+yaml")
	if err != nil {
		return errf("%v", err)
	}
	return tools.Result{Content: fmt.Sprintf("Applied %s/%s in %s.\n%s",
		kind, name, ns, summarize(data))}
}

func (t ApplyTool) delete(ctx context.Context, c *Cluster, a applyArgs) tools.Result {
	if a.Resource == "" || a.Name == "" {
		return errf("delete needs resource and name.")
	}
	path, err := resourcePath(c, a.Resource, a.Namespace, a.Name)
	if err != nil {
		return errf("%v", err)
	}
	if _, err := c.Do(ctx, "DELETE", path, nil); err != nil {
		return errf("%v", err)
	}
	return tools.Result{Content: fmt.Sprintf("Deleted %s/%s.", a.Resource, a.Name)}
}

func (t ApplyTool) scale(ctx context.Context, c *Cluster, a applyArgs) tools.Result {
	if a.Resource == "" || a.Name == "" || a.Replicas == nil {
		return errf("scale needs resource, name and replicas.")
	}
	path, err := resourcePath(c, a.Resource, a.Namespace, a.Name)
	if err != nil {
		return errf("%v", err)
	}
	body := []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, *a.Replicas))
	if _, err := c.doPatch(ctx, path+"/scale", body, "application/merge-patch+json"); err != nil {
		return errf("%v", err)
	}
	return tools.Result{Content: fmt.Sprintf("Scaled %s/%s to %d replicas.",
		a.Resource, a.Name, *a.Replicas)}
}

func (t ApplyTool) restart(ctx context.Context, c *Cluster, a applyArgs) tools.Result {
	if a.Resource == "" || a.Name == "" {
		return errf("restart needs resource and name.")
	}
	path, err := resourcePath(c, a.Resource, a.Namespace, a.Name)
	if err != nil {
		return errf("%v", err)
	}
	// The same annotation kubectl rollout restart sets.
	body := []byte(`{"spec":{"template":{"metadata":{"annotations":` +
		`{"titan.restartedAt":"` + nowRFC3339() + `"}}}}}`)
	if _, err := c.doPatch(ctx, path, body, "application/strategic-merge-patch+json"); err != nil {
		return errf("%v", err)
	}
	return tools.Result{Content: fmt.Sprintf("Restarted %s/%s.", a.Resource, a.Name)}
}

// ---------------------------------------------------------------- rendering

// render turns an API list into a compact table. Returning raw JSON would
// spend thousands of tokens on managedFields and resourceVersion, which no
// question ever needs.
func render(resource string, data []byte) string {
	var list struct {
		Kind  string           `json:"kind"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil || list.Items == nil {
		// A single object: strip the noisiest fields and return it.
		return summarize(data)
	}
	if len(list.Items) == 0 {
		return "No " + resource + " found."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d %s:\n", len(list.Items), resource)
	for _, item := range list.Items {
		meta, _ := item["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		ns, _ := meta["namespace"].(string)
		line := name
		if ns != "" {
			line = ns + "/" + name
		}
		if s := statusOf(item); s != "" {
			line += "  " + s
		}
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}

// statusOf extracts the one or two facts that matter per resource kind.
func statusOf(item map[string]any) string {
	status, _ := item["status"].(map[string]any)
	if status == nil {
		return ""
	}
	var parts []string
	if phase, ok := status["phase"].(string); ok {
		parts = append(parts, phase)
	}
	// Pods: ready containers and restarts, which is what a person looks at.
	if cs, ok := status["containerStatuses"].([]any); ok {
		ready, restarts := 0, 0
		for _, e := range cs {
			c, _ := e.(map[string]any)
			if r, _ := c["ready"].(bool); r {
				ready++
			}
			if n, ok := c["restartCount"].(float64); ok {
				restarts += int(n)
			}
		}
		parts = append(parts, fmt.Sprintf("%d/%d ready", ready, len(cs)))
		if restarts > 0 {
			parts = append(parts, strconv.Itoa(restarts)+" restarts")
		}
	}
	// Deployments.
	if r, ok := status["readyReplicas"].(float64); ok {
		if total, ok := status["replicas"].(float64); ok {
			parts = append(parts, fmt.Sprintf("%d/%d replicas", int(r), int(total)))
		}
	}
	return strings.Join(parts, "  ")
}

// summarize removes the fields that dominate a manifest's size and carry no
// information a question depends on.
func summarize(data []byte) string {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return string(data)
	}
	if meta, ok := obj["metadata"].(map[string]any); ok {
		for _, noise := range []string{"managedFields", "resourceVersion", "uid",
			"generation", "selfLink", "creationTimestamp"} {
			delete(meta, noise)
		}
	}
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return string(data)
	}
	if len(out) > 24000 {
		return string(out[:24000]) + "\n…[truncated; ask for a specific field]"
	}
	return string(out)
}

// ---------------------------------------------------------------- paths

var coreResources = map[string]bool{
	"pods": true, "services": true, "nodes": true, "namespaces": true,
	"events": true, "configmaps": true, "secrets": true,
	"persistentvolumes": true, "persistentvolumeclaims": true,
	"serviceaccounts": true, "endpoints": true, "replicationcontrollers": true,
}

var appsResources = map[string]bool{
	"deployments": true, "statefulsets": true, "daemonsets": true, "replicasets": true,
}

var clusterScoped = map[string]bool{
	"nodes": true, "namespaces": true, "persistentvolumes": true,
	"clusterroles": true, "clusterrolebindings": true, "storageclasses": true,
}

func resourcePath(c *Cluster, resource, namespace, name string) (string, error) {
	r := strings.ToLower(strings.TrimSpace(resource))
	r = normalizeResource(r)

	var base string
	switch {
	case coreResources[r]:
		base = "/api/v1"
	case appsResources[r]:
		base = "/apis/apps/v1"
	case r == "jobs":
		base = "/apis/batch/v1"
	case r == "cronjobs":
		base = "/apis/batch/v1"
	case r == "ingresses":
		base = "/apis/networking.k8s.io/v1"
	default:
		return "", fmt.Errorf("resource %q is not one Titan knows how to address. "+
			"Supported: %s. For anything else, use bash with kubectl",
			resource, strings.Join(knownResources(), ", "))
	}

	if clusterScoped[r] {
		if name != "" {
			return base + "/" + r + "/" + name, nil
		}
		return base + "/" + r, nil
	}

	ns := namespace
	if ns == "" {
		ns = c.Namespace
	}
	if ns == "*" {
		if name != "" {
			return "", fmt.Errorf("naming a single %s needs a namespace, not '*'", r)
		}
		return base + "/" + r, nil
	}
	if name != "" {
		return base + "/namespaces/" + ns + "/" + r + "/" + name, nil
	}
	return base + "/namespaces/" + ns + "/" + r, nil
}

// normalizeResource accepts the singular and short forms people type.
func normalizeResource(r string) string {
	switch r {
	case "po", "pod":
		return "pods"
	case "deploy", "deployment":
		return "deployments"
	case "svc", "service":
		return "services"
	case "ns", "namespace":
		return "namespaces"
	case "no", "node":
		return "nodes"
	case "cm", "configmap":
		return "configmaps"
	case "sts", "statefulset":
		return "statefulsets"
	case "ds", "daemonset":
		return "daemonsets"
	case "rs", "replicaset":
		return "replicasets"
	case "ing", "ingress":
		return "ingresses"
	case "job":
		return "jobs"
	case "cj", "cronjob":
		return "cronjobs"
	case "ev", "event":
		return "events"
	case "pvc":
		return "persistentvolumeclaims"
	case "pv":
		return "persistentvolumes"
	case "sa":
		return "serviceaccounts"
	case "secret":
		return "secrets"
	}
	return r
}

func knownResources() []string {
	set := map[string]bool{"jobs": true, "cronjobs": true, "ingresses": true}
	for k := range coreResources {
		set[k] = true
	}
	for k := range appsResources {
		set[k] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func apiBase(apiVersion string) string {
	if apiVersion == "v1" {
		return "/api/v1"
	}
	return "/apis/" + apiVersion
}

func pluralFor(kind string) string {
	k := strings.ToLower(kind)
	switch {
	case strings.HasSuffix(k, "s"):
		return k + "es"
	case strings.HasSuffix(k, "y"):
		return k[:len(k)-1] + "ies"
	}
	return k + "s"
}

func isNamespaced(kind string) bool {
	switch strings.ToLower(kind) {
	case "namespace", "node", "persistentvolume", "clusterrole",
		"clusterrolebinding", "storageclass", "customresourcedefinition":
		return false
	}
	return true
}

func errf(format string, a ...any) tools.Result {
	return tools.Result{Content: fmt.Sprintf(format, a...), IsError: true}
}

func urlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '~', r == '=', r == ',':
			b.WriteRune(r)
		default:
			for _, c := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
	}
	return b.String()
}

// ---------------------------------------------------------------- login tool

// LoginTool accepts a cluster credential supplied during a conversation.
//
// This exists because of a real failure: a user pasted an `oc login --token=...
// --server=...` command into the chat, approved the agent running it, and got
// nothing. Three things had gone wrong at once. The sandbox blocks reads of
// ~/.kube, so `oc` could not authenticate. Even had it worked, each bash call
// is a fresh sandboxed process, so the login would not have survived to the
// next call. And the kubeconfig's own token had expired, so the native tools
// were failing too.
//
// Handling the credential directly fixes all three: it never touches the
// sandbox, it lives in the manager for the life of the process, and it
// replaces the stale kubeconfig entry.
//
// The token is held in memory only. It is never written to the kubeconfig, the
// event store, or a log — a credential pasted into a chat should not become a
// durable artifact of that chat.
type LoginTool struct{ M *Manager }

func (LoginTool) Name() string { return "k8s_login" }

// Mutates is true. Nothing in the cluster changes, but the agent's authority
// does: this is the call that decides which cluster it can reach and as whom.
// That deserves the same confirmation as a write.
func (LoginTool) Mutates() bool { return true }

func (LoginTool) Description() string {
	return "Authenticate to a Kubernetes or OpenShift cluster with a token, for this " +
		"session only. Use this when the user supplies a token and server — including " +
		"when they paste an `oc login --token=... --server=...` command. " +
		"Do NOT run `oc login` through bash: the sandbox blocks access to the kubeconfig, " +
		"and a login inside a bash call does not survive to the next one."
}

func (LoginTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "server":{"type":"string","description":"API server URL, e.g. https://api.cluster.example.com:6443"},
    "token":{"type":"string","description":"Bearer token, e.g. sha256~..."},
    "namespace":{"type":"string","description":"Default namespace for later calls."}
  },
  "required":["server","token"]
}`)
}

type loginArgs struct {
	Server    string `json:"server"`
	Token     string `json:"token"`
	Namespace string `json:"namespace"`
}

func (t LoginTool) Run(ctx context.Context, _ *tools.Session, raw json.RawMessage) tools.Result {
	var a loginArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errf("Invalid arguments for k8s_login: %v", err)
	}
	a.Server = strings.TrimSpace(a.Server)
	a.Token = strings.TrimSpace(a.Token)
	if a.Server == "" || a.Token == "" {
		return errf("Both server and token are required.")
	}
	if !strings.HasPrefix(a.Server, "http") {
		a.Server = "https://" + a.Server
	}

	c, err := OpenDirect(a.Server, a.Token, orDefaultNS(a.Namespace, t.M.cfg.Namespace))
	if err != nil {
		return errf("%v", err)
	}
	// Verify before reporting success. Storing a credential that does not work
	// would turn one clear failure into a confusing one on the next call.
	if _, err := c.Do(ctx, "GET", "/version", nil); err != nil {
		return errf("Could not authenticate to %s: %v", a.Server, err)
	}

	t.M.login(a.Server, a.Token)
	return tools.Result{Content: fmt.Sprintf(
		"Authenticated to %s (namespace %s). This credential is held in memory for "+
			"this Titan process only and is not written to your kubeconfig. "+
			"k8s_get will now use it.", a.Server, c.Namespace)}
}

func orDefaultNS(a, b string) string {
	if a != "" {
		return a
	}
	if b != "" {
		return b
	}
	return "default"
}
