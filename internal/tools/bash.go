package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	defaultTimeoutMS = 120_000
	maxTimeoutMS     = 600_000
	maxOutputChars   = 30_000
)

// Bash runs a shell command in the session workspace.
//
// In production this executes inside the session microVM (docs §03-security).
// The guards here are defense in depth, not the boundary: they catch the
// obvious footguns early and give the model a useful message, but isolation is
// what actually contains a hostile command.
type Bash struct {
	// Sandbox, when set, wraps the command (e.g. a microVM or container exec).
	// Nil means direct execution, which is only appropriate for local dev.
	Sandbox func(ctx context.Context, cwd, command string) *exec.Cmd
}

func (Bash) Name() string  { return "bash" }
func (Bash) Mutates() bool { return true }

func (Bash) Description() string {
	return "Run a shell command in the session workspace. Use for builds, tests, git, and package managers. Prefer read/glob/grep for file inspection — they are cheaper and safer. Note: the working directory persists between calls, but shell state (variables, functions) does not."
}

func (Bash) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "command":{"type":"string","description":"The shell command to run."},
    "description":{"type":"string","description":"Short human-readable description of what this does, shown in the approval prompt."},
    "timeout_ms":{"type":"integer","description":"Timeout in milliseconds. Default 120000, max 600000."}
  },
  "required":["command","description"]
}`)
}

type bashArgs struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	TimeoutMS   int    `json:"timeout_ms"`
}

// Commands that hang forever waiting for a TTY. Rejecting them with guidance
// is far better than a 10-minute timeout.
var interactivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bgit\s+(rebase|add|commit)\s+.*-i\b`),
	regexp.MustCompile(`\bgit\s+(rebase|add)\s+--interactive\b`),
	regexp.MustCompile(`^\s*(vim?|nano|emacs|less|more|top|htop)\b`),
	// ssh without -T allocates a TTY and blocks; -T is the non-interactive form.
	regexp.MustCompile(`^\s*ssh\s+(?:-[^T\s]*\s+)*[^-\s]`),
}

// Patterns that require confirmation in every mode, including the most
// permissive (docs P7). These are the commands with no undo.
var destructivePatterns = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`\brm\s+(-[a-zA-Z]*\s+)*-[a-zA-Z]*[rf]`), "recursive/forced delete"},
	{regexp.MustCompile(`\bgit\s+push\s+.*--force(?:-with-lease)?\b`), "force push"},
	{regexp.MustCompile(`\bgit\s+reset\s+--hard\b`), "hard reset"},
	{regexp.MustCompile(`\bgit\s+clean\s+.*-[a-zA-Z]*f`), "forced clean"},
	{regexp.MustCompile(`\b(mkfs|fdisk|dd)\b`), "disk operation"},
	{regexp.MustCompile(`\bchmod\s+(-R\s+)?777\b`), "world-writable permissions"},
	{regexp.MustCompile(`:\(\)\s*\{.*\}\s*;?\s*:`), "fork bomb"},
	{regexp.MustCompile(`\b(shutdown|reboot|halt)\b`), "system power command"},
	{regexp.MustCompile(`>\s*/dev/(sd|nvme|disk)`), "raw device write"},
	// Deletion without `rm`. Found by TestAttack_CommandWrappingBypass, which
	// tried wrapped forms of a denied command: `find . -delete` recursively
	// removes files while containing none of the patterns above.
	{regexp.MustCompile(`\bfind\b.*\s-delete\b`), "recursive delete via find"},
	{regexp.MustCompile(`\bfind\b.*-exec\s+rm\b`), "recursive delete via find -exec"},
	{regexp.MustCompile(`\bxargs\b.*\brm\b`), "delete via xargs"},
	{regexp.MustCompile(`\bshred\b`), "secure delete"},
	{regexp.MustCompile(`\btruncate\s+-s\s*0\b`), "file truncation"},
	{regexp.MustCompile(`\bgit\s+checkout\s+--\s+\.`), "discard all working-tree changes"},
	{regexp.MustCompile(`\bgit\s+branch\s+-D\b`), "force branch delete"},
}

// Note on completeness: this list cannot be exhaustive. Shell affords endless
// ways to express deletion, and pattern matching on command text will always
// lag. That is precisely why the sandbox — not this list — is the actual
// boundary (docs/architecture/03-security.md). These patterns exist to make the
// common destructive cases require confirmation, not to be a security control
// anything depends on.

// IsDestructive reports whether a command needs confirmation regardless of
// permission mode. Exported so the policy engine can consult it.
func IsDestructive(command string) (string, bool) {
	for _, d := range destructivePatterns {
		if d.re.MatchString(command) {
			return d.what, true
		}
	}
	return "", false
}

func (b Bash) Run(ctx context.Context, s *Session, raw json.RawMessage) Result {
	var a bashArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errf("Invalid arguments for bash: %v", err)
	}
	if strings.TrimSpace(a.Command) == "" {
		return errf("command is required.")
	}
	if strings.TrimSpace(a.Description) == "" {
		return errf("description is required — it is shown to the user in the approval prompt. Describe what the command does in a few words.")
	}

	for _, re := range interactivePatterns {
		if re.MatchString(a.Command) {
			return errf("Refusing to run an interactive command: %s\nInteractive commands wait for a terminal that is not attached and will hang.\nUse a non-interactive equivalent (for example `git rebase --onto` instead of `git rebase -i`, or `cat` instead of `less`).", a.Command)
		}
	}

	timeout := time.Duration(a.TimeoutMS) * time.Millisecond
	if a.TimeoutMS <= 0 {
		timeout = defaultTimeoutMS * time.Millisecond
	}
	if timeout > maxTimeoutMS*time.Millisecond {
		timeout = maxTimeoutMS * time.Millisecond
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if b.Sandbox != nil {
		cmd = b.Sandbox(runCtx, s.Cwd, a.Command)
	} else {
		cmd = exec.CommandContext(runCtx, "bash", "-c", a.Command)
		cmd.Dir = s.Cwd
		// Minimal environment: the agent should not inherit the operator's
		// credentials by accident.
		cmd.Env = append(os.Environ(), "TITAN_SESSION=1")
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	content := out.String()
	truncated := false
	if len(content) > maxOutputChars {
		// Keep head and tail: the command's intent is at the start, the error
		// is almost always at the end.
		head := content[:maxOutputChars/2]
		tail := content[len(content)-maxOutputChars/2:]
		content = fmt.Sprintf("%s\n\n[... %d characters truncated ...]\n\n%s",
			head, len(out.String())-maxOutputChars, tail)
		truncated = true
	}

	if runCtx.Err() == context.DeadlineExceeded {
		return Result{
			Content: fmt.Sprintf("Command timed out after %s.\n%s\n\nIf this command is expected to run long, raise timeout_ms (max %d).",
				timeout, content, maxTimeoutMS),
			IsError:   true,
			Truncated: truncated,
		}
	}
	if ctx.Err() != nil {
		return Result{Content: "Command cancelled.\n" + content, IsError: true}
	}

	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok {
			exitCode = ee.ExitCode()
		} else {
			return errf("Failed to run command: %v", err)
		}
	}

	// Track cd so the working directory persists across calls, matching the
	// documented contract. Shell state deliberately does not persist.
	if newCwd := detectCd(a.Command, s); newCwd != "" {
		s.Cwd = newCwd
	}

	if content == "" {
		content = "[no output]"
	}

	// A non-zero exit is a valid observation the model must reason about, not a
	// tool failure. Never convert a failing test run into an error.
	header := fmt.Sprintf("exit %d · %s", exitCode, elapsed.Round(time.Millisecond))
	return Result{
		Content:   header + "\n" + content,
		Truncated: truncated,
		ExitCode:  &exitCode,
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

// detectCd resolves a trailing `cd` so the next call starts where this one
// ended. Only handles the simple leading/trailing forms models actually emit.
func detectCd(command string, s *Session) string {
	parts := strings.Split(command, "&&")
	last := strings.TrimSpace(parts[len(parts)-1])
	if !strings.HasPrefix(last, "cd ") {
		return ""
	}
	target := strings.TrimSpace(strings.TrimPrefix(last, "cd "))
	target = strings.Trim(target, `"'`)
	if target == "" || target == "~" {
		return s.Root
	}
	if !strings.HasPrefix(target, "/") {
		target = s.Cwd + "/" + target
	}
	resolved, err := s.Resolve(target)
	if err != nil {
		return "" // refuse to cd outside the workspace
	}
	if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
		return ""
	}
	return resolved
}
