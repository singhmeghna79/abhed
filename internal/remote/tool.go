package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yuvrajsingh/abhed/internal/tools"
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
// the consequence — on a remote host Abhed cannot see, the two are not the
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
			"Abhed cannot connect to a host that is not declared.",
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

// Add registers a host discovered during a conversation. Operator-equivalent
// input: it comes from what the USER typed, not from anything the model read.
func (r *Registry) Add(h *Host) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts[h.Name()] = h
}

// ---------------------------------------------------------------- connect

// ConnectTool declares a machine during a conversation.
//
// This exists for the same reason k8s_login does: a user pasting "connect to
// 52.116.120.159, key is at ~/Downloads/id_rsa" is giving a credential, and
// without somewhere to put it the agent falls back to `ssh` through bash —
// where the sandbox denies the key read, the connection cannot persist between
// calls, and the failure is opaque.
//
// The key is read by Abhed, outside the sandbox, and the host is remembered for
// the life of the process. Nothing is written to ~/.ssh/config.
type ConnectTool struct{ R *Registry }

func (ConnectTool) Name() string { return "ssh_connect" }

// Mutates is true: this decides which machines the agent can reach and as
// whom, which deserves the same confirmation as a write.
func (ConnectTool) Mutates() bool { return true }

func (ConnectTool) Description() string {
	return "Register a remote machine for SSH, for this session only. Use when the user " +
		"gives an address and a key path or password. Call this FIRST with the path " +
		"exactly as the user wrote it — it resolves typos and common locations itself, " +
		"so do not search the filesystem for the key beforehand. " +
		"Do NOT use `ssh` through bash: the sandbox denies reads of key material, and a " +
		"connection made inside a bash call does not survive to the next one. " +
		"After this succeeds, run commands with the `ssh` tool."
}

func (ConnectTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "name":{"type":"string","description":"Short name to refer to this host later. Defaults to the address."},
    "addr":{"type":"string","description":"Hostname or IP, optionally host:port."},
    "user":{"type":"string","description":"Login user. Defaults to root."},
    "identity_file":{"type":"string","description":"Path to the private key, as the user gave it."},
    "password_env":{"type":"string","description":"Environment variable holding a password, if there is no key."},
    "accept_host_key":{"type":"boolean","description":"Accept the host key on first sight. Only set this when the user has said the host is new or ephemeral."}
  },
  "required":["addr"]
}`)
}

type connectArgs struct {
	Name          string `json:"name"`
	Addr          string `json:"addr"`
	User          string `json:"user"`
	IdentityFile  string `json:"identity_file"`
	PasswordEnv   string `json:"password_env"`
	AcceptHostKey bool   `json:"accept_host_key"`
}

func (t ConnectTool) Run(ctx context.Context, _ *tools.Session, raw json.RawMessage) tools.Result {
	var a connectArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errf("Invalid arguments for ssh_connect: %v", err)
	}
	if strings.TrimSpace(a.Addr) == "" {
		return errf("addr is required.")
	}
	if a.User == "" {
		a.User = "root"
	}
	if a.Name == "" {
		a.Name = a.Addr
	}

	if a.IdentityFile != "" {
		// A path typed into a chat is approximate: "~Dowloads/key (1).prv" was
		// a real example, with a missing slash and a typo. Resolving it here
		// beats making the agent guess with glob.
		resolved, err := resolveKeyPath(a.IdentityFile)
		if err != nil {
			return errf("%v", err)
		}
		a.IdentityFile = resolved
	}

	h, err := NewHost(HostConfig{
		Name: a.Name, Addr: a.Addr, User: a.User,
		IdentityFile:             a.IdentityFile,
		PasswordEnv:              a.PasswordEnv,
		InsecureSkipHostKeyCheck: a.AcceptHostKey,
	})
	if err != nil {
		return errf("%v", err)
	}

	// Verify before reporting success: storing a host that cannot be reached
	// turns one clear failure into a confusing one on the next command.
	out, err := h.Run(ctx, "echo abhed-connected", 30*time.Second)
	if err != nil {
		h.Close()
		return errf("%v", err)
	}
	if !strings.Contains(out.Stdout, "abhed-connected") {
		h.Close()
		return errf("Connected to %s but the host did not run a command as expected.", a.Addr)
	}

	t.R.Add(h)
	key := "the ssh agent"
	if a.IdentityFile != "" {
		key = a.IdentityFile
	}
	return tools.Result{Content: fmt.Sprintf(
		"Connected to %s@%s as %q using %s. This host is registered for this Abhed "+
			"process only and is not written to ~/.ssh/config. "+
			"Run commands on it with the ssh tool.", a.User, a.Addr, a.Name, key)}
}

// resolveKeyPath finds a key from a path a person typed.
//
// People paste paths with a missing slash after ~, a misspelled directory, or
// a shell-unfriendly name like "key (1).prv". Failing on the literal string
// would send the agent hunting with glob through directories the sandbox
// denies, which is exactly the loop this tool exists to avoid.
func resolveKeyPath(path string) (string, error) {
	candidates := []string{expandHome(path)}

	// "~Downloads/x" is missing its slash.
	if strings.HasPrefix(path, "~") && !strings.HasPrefix(path, "~/") {
		candidates = append(candidates, expandHome("~/"+strings.TrimPrefix(path, "~")))
	}
	// A bare filename, or a misspelled parent: look in the usual places.
	base := filepath.Base(path)
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range []string{"Downloads", "Desktop", ".ssh", ""} {
			candidates = append(candidates, filepath.Join(home, dir, base))
		}
	}

	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("no key file found at %q. Tried: %s. "+
		"Give the full path, or check the spelling of the directory",
		path, strings.Join(uniq(candidates), ", "))
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
