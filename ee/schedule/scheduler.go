package schedule

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Job is one schedule, as the operator wrote it.
type Job struct {
	Name     string
	Cron     string
	Prompt   string
	Mode     string
	Provider string
	Disabled bool
}

// Runner starts a session for a job and returns its ID. It is expected to
// return once the session is STARTED, not finished; the scheduler does not
// wait on the agent, only on the runner.
type Runner func(ctx context.Context, job Job) (sessionID string, err error)

// Status is what the admin view shows for one job.
type Status struct {
	Name        string    `json:"name"`
	Cron        string    `json:"cron"`
	Prompt      string    `json:"prompt"`
	Mode        string    `json:"mode,omitempty"`
	Disabled    bool      `json:"disabled"`
	NextRun     time.Time `json:"next_run"`
	LastRun     time.Time `json:"last_run,omitempty"`
	LastSession string    `json:"last_session,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	Running     bool      `json:"running"`
	Runs        int       `json:"runs"`
	Skipped     int       `json:"skipped"`
}

type jobState struct {
	Job
	expr    *Expr
	running bool
	last    time.Time
	lastID  string
	lastErr string
	runs    int
	skipped int
}

// Scheduler fires jobs on their timetable.
type Scheduler struct {
	mu   sync.Mutex
	jobs []*jobState
	run  Runner
	log  *slog.Logger
	now  func() time.Time
}

// New parses every job's expression up front, so a typo in a schedule is a
// startup error rather than a job that silently never fires.
func New(jobs []Job, run Runner, log *slog.Logger) (*Scheduler, error) {
	if log == nil {
		log = slog.Default()
	}
	s := &Scheduler{run: run, log: log, now: time.Now}
	seen := map[string]bool{}
	for _, j := range jobs {
		if j.Name == "" {
			return nil, fmt.Errorf("schedule: every job needs a name")
		}
		if seen[j.Name] {
			return nil, fmt.Errorf("schedule %q: duplicate name", j.Name)
		}
		seen[j.Name] = true
		if j.Prompt == "" {
			return nil, fmt.Errorf("schedule %q: prompt is required", j.Name)
		}
		e, err := Parse(j.Cron)
		if err != nil {
			return nil, fmt.Errorf("schedule %q: %w", j.Name, err)
		}
		s.jobs = append(s.jobs, &jobState{Job: j, expr: e})
	}
	return s, nil
}

// Bind sets the runner. The server and the scheduler each need the other —
// the server starts runs, the scheduler lists them — so one is constructed
// first and the runner is bound once both exist.
func (s *Scheduler) Bind(run Runner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.run = run
}

// Start runs the clock until ctx ends. It checks once per minute, on the
// minute, which is the resolution of a cron expression.
func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		for {
			now := s.now()
			next := now.Truncate(time.Minute).Add(time.Minute)
			select {
			case <-ctx.Done():
				return
			case <-time.After(next.Sub(now)):
			}
			s.tick(ctx, s.now())
		}
	}()
}

// tick fires every job due at t. Exposed to tests through the package.
func (s *Scheduler) tick(ctx context.Context, t time.Time) {
	s.mu.Lock()
	var due []*jobState
	for _, j := range s.jobs {
		if j.Disabled || !j.expr.Matches(t) {
			continue
		}
		if j.running {
			// The previous run has not finished. Overlapping runs of the same
			// job are almost never what anyone meant — two agents doing the
			// same nightly task at once — so this one is skipped and counted.
			j.skipped++
			s.log.Warn("schedule skipped: previous run still going", "job", j.Name)
			continue
		}
		j.running = true
		due = append(due, j)
	}
	s.mu.Unlock()
	for _, j := range due {
		s.fire(ctx, j, "cron")
	}
}

func (s *Scheduler) fire(ctx context.Context, j *jobState, how string) {
	s.mu.Lock()
	run := s.run
	s.mu.Unlock()
	if run == nil {
		s.mu.Lock()
		j.running, j.lastErr = false, "scheduler has no runner bound"
		s.mu.Unlock()
		s.log.Error("schedule cannot fire: no runner bound", "job", j.Name)
		return
	}
	id, err := run(ctx, j.Job)
	s.mu.Lock()
	j.last = s.now()
	j.runs++
	j.lastID = id
	if err != nil {
		j.lastErr = err.Error()
		j.running = false
		s.log.Error("schedule failed to start", "job", j.Name, "err", err)
	} else {
		j.lastErr = ""
		s.log.Info("schedule started", "job", j.Name, "session", id, "by", how)
	}
	s.mu.Unlock()
}

// Finished is called by the runner's owner when a job's session ends, so the
// next tick may fire it again.
func (s *Scheduler) Finished(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.Name == name {
			j.running = false
		}
	}
}

// RunNow fires a job outside its timetable — the admin's "run it now" button.
// It refuses if the job is already running, for the same reason tick skips.
func (s *Scheduler) RunNow(ctx context.Context, name string) (string, error) {
	s.mu.Lock()
	var j *jobState
	for _, c := range s.jobs {
		if c.Name == name {
			j = c
		}
	}
	if j == nil {
		s.mu.Unlock()
		return "", fmt.Errorf("no schedule named %q", name)
	}
	if j.running {
		s.mu.Unlock()
		return "", fmt.Errorf("schedule %q is already running", name)
	}
	j.running = true
	s.mu.Unlock()
	s.fire(ctx, j, "admin")
	s.mu.Lock()
	defer s.mu.Unlock()
	if j.lastErr != "" {
		return "", fmt.Errorf("%s", j.lastErr)
	}
	return j.lastID, nil
}

// Status reports every job, next-run first.
func (s *Scheduler) Status() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	out := make([]Status, 0, len(s.jobs))
	for _, j := range s.jobs {
		st := Status{
			Name: j.Name, Cron: j.Cron, Prompt: j.Prompt, Mode: j.Mode,
			Disabled: j.Disabled, LastRun: j.last, LastSession: j.lastID,
			LastError: j.lastErr, Running: j.running, Runs: j.runs, Skipped: j.skipped,
		}
		if !j.Disabled {
			st.NextRun = j.expr.Next(now)
		}
		out = append(out, st)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].NextRun.IsZero() != out[b].NextRun.IsZero() {
			return !out[a].NextRun.IsZero()
		}
		return out[a].NextRun.Before(out[b].NextRun)
	})
	return out
}
