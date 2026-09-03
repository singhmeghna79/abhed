package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yuvrajsingh/titan/internal/tools"
)

// Registry holds the hosts an operator has declared.
type Registry struct {
	mu    sync.RWMutex
	hosts map[string]*Host
}

func NewRegistry(configs []HostConfig) (*Registry, []error) {
	r := &Registry{hosts: map[string]*Host{}}
	var errs []error
	for _, c := range configs {
		h, err := NewHost(c)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		r.hosts[c.Name] = h
	}
	return r, errs
}

func (r *Registry) get(name string) (*Host, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.hosts[name]
	return h, ok
}

func (r *Registry) names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.hosts))
	for n := range r.hosts {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.hosts)
}

func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range r.hosts {
		h.Close()
	}
}

// Tool runs a command on a declared host.
type Tool struct{ R *Registry }

func (Tool) Name() string { return "ssh" }

// Mutates is true unconditionally.
//
// A local bash call can be judged by its text because it runs inside a sandbox
// with a workspace boundary and a checkpoint behind it. None of that is true
// over SSH: the command runs with the remote account's full authority, and
// there is no undo. Classifying `cat` as safe would be judging the string, not
// the consequence — on a remote host Titan cannot see, the two are not the
// same thing. So every remote command asks.
func (Tool) Mutates() bool { return true }

func (t Tool) Description() string {
	hosts := ""
	if t.R != nil && t.R.Len() > 0 {
		hosts = " Declared hosts: " + strings.Join(t.R.names(), ", ") + "."
	}
	return "Run a shell command on a remote machine over SSH." + hosts +
		" Every call requires approval, because a remote command runs outside " +
		"the sandbox with no undo. Prefer one command that answers the question " +
		"over several exploratory ones."
}

func (Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "host":{"type":"string","description":"Name of a configured host."},
    "command":{"type":"string","description":"The shell command to run."},
    "timeout_seconds":{"type":"integer","description":"How long to wait. Default 120."}
  },
  "required":["host","command"]
}`)
}

type sshArgs struct {
	Host    string `json:"host"`
	Command string `json:"command"`
	Timeout int    `json:"timeout_seconds"`
}

func (t Tool) Run(ctx context.Context, _ *tools.Session, raw json.RawMessage) tools.Result {
	var a sshArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errf("Invalid arguments for ssh: %v", err)
	}
	if t.R == nil || t.R.Len() == 0 {
		return errf("No SSH hosts are configured. An operator declares them in " +
			"ssh.hosts; the agent cannot add one.")
	}
	if strings.TrimSpace(a.Host) == "" {
		return errf("host is required. Configured: %s", strings.Join(t.R.names(), ", "))
	}
	if strings.TrimSpace(a.Command) == "" {
		return errf("command is required.")
	}

	h, ok := t.R.get(a.Host)
	if !ok {
		// Naming the alternatives ends the retry loop a bare "not found"
		// otherwise causes.
		return errf("No host named %q. Configured hosts: %s. "+
			"Titan cannot connect to a host that is not declared.",
			a.Host, strings.Join(t.R.names(), ", "))
	}

	timeout := time.Duration(a.Timeout) * time.Second
	out, err := h.Run(ctx, a.Command, timeout)
	if err != nil {
		if out != nil && (out.Stdout != "" || out.Stderr != "") {
			return errf("%v\n%s", err, combine(out))
		}
		return errf("%v", err)
	}

	body := combine(out)
	if body == "" {
		body = "(no output)"
	}
	code := out.ExitCode
	return tools.Result{
		Content:  fmt.Sprintf("%s@%s · exit %d\n%s", h.User(), h.Name(), code, body),
		IsError:  code != 0,
		ExitCode: &code,
	}
}

func combine(o *Output) string {
	var b strings.Builder
	if s := strings.TrimRight(o.Stdout, "\n"); s != "" {
		b.WriteString(s)
	}
	if s := strings.TrimRight(o.Stderr, "\n"); s != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("stderr: " + s)
	}
	return b.String()
}

func errf(format string, a ...any) tools.Result {
	return tools.Result{Content: fmt.Sprintf(format, a...), IsError: true}
}
