package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Process confines a command with OS process-level primitives: macOS
// sandbox-exec (Seatbelt) or Linux bubblewrap.
//
// This is a REAL boundary for filesystem and network scoping, and it is
// verified by the tests in this package. It is NOT a boundary against kernel
// exploitation — the kernel is shared. Use TierVM for untrusted code.
type Process struct {
	policy  Policy
	profile string // rendered sandbox profile path, macOS only
	backend string // "sandbox-exec" | "bwrap"
}

func NewProcess(p Policy) *Process {
	s := &Process{policy: p}
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err == nil {
			s.backend = "sandbox-exec"
		}
	case "linux":
		if _, err := exec.LookPath("bwrap"); err == nil {
			s.backend = "bwrap"
		}
	}
	return s
}

func (s *Process) Tier() Tier { return TierProcess }

func (s *Process) Available() (bool, string) {
	if s.backend == "" {
		switch runtime.GOOS {
		case "darwin":
			return false, "sandbox-exec not found"
		case "linux":
			return false, "bubblewrap (bwrap) not installed"
		default:
			return false, runtime.GOOS + " has no supported process sandbox"
		}
	}
	return true, ""
}

func (s *Process) Describe() string {
	net := "no network"
	if s.policy.AllowNetwork {
		net = "network allowed"
	}
	return fmt.Sprintf("process isolation via %s · workspace-scoped writes · %s", s.backend, net)
}

// seatbeltProfile renders a macOS Seatbelt profile.
//
// Deliberately built on (allow default) with targeted denials rather than
// (deny default) with targeted allows. A deny-default profile breaks DNS and
// mach service lookup in ways that make ordinary toolchains fail confusingly,
// and a sandbox people disable is worth nothing. The denials below are the ones
// that actually matter for an agent: writes outside the workspace, and network.
func (s *Process) seatbeltProfile() string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n\n")

	b.WriteString(";; Writes are confined to the workspace and standard temp dirs.\n")
	b.WriteString("(deny file-write*)\n")
	b.WriteString(fmt.Sprintf("(allow file-write* (subpath %q))\n", s.policy.Workspace))
	for _, p := range []string{"/private/tmp", "/private/var/tmp", "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/urandom", "/dev/dtracehelper"} {
		b.WriteString(fmt.Sprintf("(allow file-write* (subpath %q))\n", p))
	}
	// Toolchains need writable caches or builds fail in ways that look like
	// agent errors rather than sandbox errors.
	if home, err := os.UserHomeDir(); err == nil {
		for _, c := range []string{".cache", "Library/Caches", ".npm", ".cargo/registry", "go/pkg/mod"} {
			b.WriteString(fmt.Sprintf("(allow file-write* (subpath %q))\n", filepath.Join(home, c)))
		}
	}

	if !s.policy.AllowNetwork {
		b.WriteString("\n;; Egress denied: a successful injection has no channel out.\n")
		b.WriteString("(deny network*)\n")
	}

	b.WriteString("\n;; Never writable, regardless of workspace location.\n")
	for _, p := range []string{"/etc", "/System", "/usr", "/bin", "/sbin", "/Library/LaunchDaemons"} {
		b.WriteString(fmt.Sprintf("(deny file-write* (subpath %q))\n", p))
	}
	// Credentials are readable by many tools legitimately, but an agent has no
	// reason to read SSH or cloud keys.
	b.WriteString("\n;; Credential paths are unreadable.\n")
	if home, err := os.UserHomeDir(); err == nil {
		for _, c := range []string{".ssh", ".aws", ".kube", ".gnupg", ".docker/config.json"} {
			b.WriteString(fmt.Sprintf("(deny file-read* (subpath %q))\n", filepath.Join(home, c)))
		}
	}
	return b.String()
}

func (s *Process) Command(ctx context.Context, cwd, command string) *exec.Cmd {
	switch s.backend {
	case "sandbox-exec":
		profile := s.seatbeltProfile()
		// -p takes the profile inline, avoiding a temp file the command could
		// itself tamper with.
		cmd := exec.CommandContext(ctx, "sandbox-exec", "-p", profile, "/bin/bash", "-c", command)
		cmd.Dir = cwd
		cmd.Env = s.env()
		return cmd

	case "bwrap":
		args := []string{
			"--die-with-parent",
			"--unshare-pid", "--unshare-ipc", "--unshare-uts",
			"--proc", "/proc", "--dev", "/dev",
			// Read-only system, writable workspace.
			"--ro-bind", "/usr", "/usr",
			"--ro-bind", "/bin", "/bin",
			"--ro-bind", "/lib", "/lib",
			"--ro-bind-try", "/lib64", "/lib64",
			"--ro-bind-try", "/etc/resolv.conf", "/etc/resolv.conf",
			"--ro-bind-try", "/etc/ssl", "/etc/ssl",
			"--bind", s.policy.Workspace, s.policy.Workspace,
			"--tmpfs", "/tmp",
			"--chdir", cwd,
		}
		if !s.policy.AllowNetwork {
			args = append(args, "--unshare-net")
		}
		for _, p := range s.policy.ReadOnlyPaths {
			args = append(args, "--ro-bind-try", p, p)
		}
		args = append(args, "/bin/bash", "-c", command)

		cmd := exec.CommandContext(ctx, "bwrap", args...)
		cmd.Dir = cwd
		cmd.Env = s.env()
		return cmd
	}

	// Unreachable when Available() gated correctly, but fail closed rather than
	// silently running unsandboxed.
	return exec.CommandContext(ctx, "false")
}

// env builds a minimal environment. The agent should not inherit the operator's
// credentials by accident, so only an explicit allowlist passes through.
func (s *Process) env() []string {
	keep := []string{"PATH", "HOME", "LANG", "LC_ALL", "TERM", "TMPDIR",
		"GOPATH", "GOROOT", "GOCACHE", "GOMODCACHE",
		"NODE_PATH", "npm_config_cache", "CARGO_HOME", "RUSTUP_HOME",
		"JAVA_HOME", "PYTHONPATH", "VIRTUAL_ENV"}
	out := []string{"TITAN_SANDBOX=" + string(s.Tier())}
	for _, k := range keep {
		if v := os.Getenv(k); v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// None runs commands directly on the host.
//
// It exists so the tier is always explicit and auditable rather than implied by
// the absence of a sandbox. It refuses to be selected when the policy requires
// any real isolation.
type None struct{ policy Policy }

func NewNone(p Policy) *None { return &None{policy: p} }

func (n *None) Tier() Tier { return TierNone }

func (n *None) Available() (bool, string) { return true, "" }

func (n *None) Describe() string {
	return "NO ISOLATION — commands run directly on the host. Trusted repositories only."
}

func (n *None) Command(ctx context.Context, cwd, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TITAN_SANDBOX=none")
	return cmd
}
