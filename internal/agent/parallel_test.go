package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir
}

// Isolated tasks each get their own worktree on their own branch; what they
// write lands there and nowhere else, and the parent is told how to merge.
func TestParallelTasksInWorktreesDoNotTouchEachOtherOrMain(t *testing.T) {
	ws := gitRepo(t)
	var seen sync.Map
	spawn := func(ctx context.Context, req SubagentRequest) (string, error) {
		if req.Workspace == "" {
			return "", os.ErrInvalid
		}
		seen.Store(req.Description, req.Workspace)
		// Each child writes a file named after itself into ITS workspace.
		return "wrote", os.WriteFile(filepath.Join(req.Workspace, req.Description+".txt"), []byte("x"), 0o644)
	}
	tool := Tasks{Spawn: spawn, Workspace: ws}
	args, _ := json.Marshal(map[string]any{
		"isolation": "worktree",
		"tasks": []map[string]any{
			{"prompt": "a", "description": "alpha"},
			{"prompt": "b", "description": "beta"},
		},
	})
	res := tool.Run(context.Background(), nil, args)
	if res.IsError {
		t.Fatalf("tasks failed: %s", res.Content)
	}

	// Two distinct worktrees, both under the workspace.
	wa, _ := seen.Load("alpha")
	wb, _ := seen.Load("beta")
	if wa == nil || wb == nil || wa == wb {
		t.Fatalf("worktrees: alpha=%v beta=%v", wa, wb)
	}
	for _, w := range []string{wa.(string), wb.(string)} {
		if !strings.HasPrefix(w, filepath.Join(ws, WorktreeDir)) {
			t.Errorf("worktree %s is outside %s", w, WorktreeDir)
		}
	}
	// alpha's file is in alpha's tree only; the main tree has neither.
	if _, err := os.Stat(filepath.Join(wa.(string), "alpha.txt")); err != nil {
		t.Error("alpha's file missing from alpha's worktree")
	}
	if _, err := os.Stat(filepath.Join(wb.(string), "alpha.txt")); err == nil {
		t.Error("alpha's file leaked into beta's worktree")
	}
	if _, err := os.Stat(filepath.Join(ws, "alpha.txt")); err == nil {
		t.Error("alpha's file leaked into the main tree")
	}
	// The report says what changed and how to take it.
	for _, want := range []string{"## Task 1 — alpha", "## Task 2 — beta", "branch titan/", "UNCOMMITTED", "git merge titan/"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("report missing %q:\n%s", want, res.Content)
		}
	}
	// The worktree directory is excluded from the main tree's status.
	out, _ := exec.Command("git", "-C", ws, "status", "--porcelain").Output()
	if strings.Contains(string(out), WorktreeDir) {
		t.Errorf("worktrees show up in git status of the main tree:\n%s", out)
	}
}

// Without isolation the children share the parent's workspace, run at the
// same time, and are bounded by MaxParallel.
func TestParallelTasksRunConcurrentlyUpToTheLimit(t *testing.T) {
	var inFlight, peak atomic.Int32
	spawn := func(ctx context.Context, req SubagentRequest) (string, error) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		inFlight.Add(-1)
		return "done " + req.Description, nil
	}
	tool := Tasks{Spawn: spawn, Workspace: t.TempDir(), MaxParallel: 2}
	args, _ := json.Marshal(map[string]any{"tasks": []map[string]any{
		{"prompt": "1", "description": "one"}, {"prompt": "2", "description": "two"},
		{"prompt": "3", "description": "three"}, {"prompt": "4", "description": "four"},
	}})
	start := time.Now()
	res := tool.Run(context.Background(), nil, args)
	if res.IsError {
		t.Fatal(res.Content)
	}
	if peak.Load() != 2 {
		t.Errorf("peak concurrency %d, want exactly the limit of 2", peak.Load())
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Error("four 30ms tasks at concurrency 2 took too long — they ran serially")
	}
	if !strings.Contains(res.Content, "done four") {
		t.Errorf("report lost a task:\n%s", res.Content)
	}
}

// Asking for isolation outside a git repository is refused with a reason,
// before any subagent has spent a token.
func TestWorktreeIsolationNeedsAGitRepo(t *testing.T) {
	spawned := false
	tool := Tasks{Spawn: func(context.Context, SubagentRequest) (string, error) { spawned = true; return "", nil },
		Workspace: t.TempDir()}
	args, _ := json.Marshal(map[string]any{"isolation": "worktree",
		"tasks": []map[string]any{{"prompt": "x", "description": "x"}}})
	res := tool.Run(context.Background(), nil, args)
	if !res.IsError || !strings.Contains(res.Content, "git repository") {
		t.Errorf("expected a refusal naming git, got: %s", res.Content)
	}
	if spawned {
		t.Error("a subagent was spawned despite the refusal")
	}
}

// One failing task does not fail the others; the report says which failed.
func TestParallelTasksReportPartialFailure(t *testing.T) {
	tool := Tasks{Spawn: func(ctx context.Context, req SubagentRequest) (string, error) {
		if req.Description == "bad" {
			return "", context.DeadlineExceeded
		}
		return "fine", nil
	}, Workspace: t.TempDir()}
	args, _ := json.Marshal(map[string]any{"tasks": []map[string]any{
		{"prompt": "a", "description": "good"}, {"prompt": "b", "description": "bad"},
	}})
	res := tool.Run(context.Background(), nil, args)
	if res.IsError {
		t.Error("a partial failure was reported as total failure")
	}
	if !strings.Contains(res.Content, "FAILED") || !strings.Contains(res.Content, "[1 of 2 tasks failed]") {
		t.Errorf("report does not name the failure:\n%s", res.Content)
	}
}
