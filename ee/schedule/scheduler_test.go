package schedule

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestTickFiresDueJobsOnly(t *testing.T) {
	var fired atomic.Int32
	s, err := New([]Job{
		{Name: "nine", Cron: "0 9 * * *", Prompt: "morning"},
		{Name: "noon", Cron: "0 12 * * *", Prompt: "lunch"},
		{Name: "off", Cron: "0 9 * * *", Prompt: "never", Disabled: true},
	}, func(ctx context.Context, j Job) (string, error) {
		fired.Add(1)
		return "s-" + j.Name, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return at("2026-09-13 09:00") }
	s.tick(context.Background(), at("2026-09-13 09:00"))
	if fired.Load() != 1 {
		t.Errorf("fired %d jobs at 09:00, want 1 (nine only; noon not due, off disabled)", fired.Load())
	}
	st := s.Status()
	for _, j := range st {
		if j.Name == "nine" && j.LastSession != "s-nine" {
			t.Errorf("nine did not record its session: %+v", j)
		}
		if j.Name == "off" && !j.NextRun.IsZero() {
			t.Error("a disabled job reports a next run")
		}
	}
}

// A job that is still running when its next firing comes round is skipped
// and counted, not started a second time alongside itself.
func TestNoOverlappingRuns(t *testing.T) {
	var fired atomic.Int32
	s, _ := New([]Job{{Name: "long", Cron: "* * * * *", Prompt: "x"}},
		func(ctx context.Context, j Job) (string, error) { fired.Add(1); return "s", nil }, nil)
	s.now = func() time.Time { return at("2026-09-13 09:00") }
	s.tick(context.Background(), at("2026-09-13 09:00"))
	s.tick(context.Background(), at("2026-09-13 09:01")) // still running: nobody called Finished
	if fired.Load() != 1 {
		t.Fatalf("job overlapped itself: fired %d", fired.Load())
	}
	if s.Status()[0].Skipped != 1 {
		t.Errorf("skipped count = %d, want 1", s.Status()[0].Skipped)
	}
	s.Finished("long")
	s.tick(context.Background(), at("2026-09-13 09:02"))
	if fired.Load() != 2 {
		t.Errorf("after Finished, job did not fire again: %d", fired.Load())
	}
}

func TestRunNowAndFailureIsRecorded(t *testing.T) {
	s, _ := New([]Job{{Name: "j", Cron: "0 0 1 1 *", Prompt: "x"}},
		func(ctx context.Context, j Job) (string, error) { return "", errors.New("provider down") }, nil)
	if _, err := s.RunNow(context.Background(), "j"); err == nil {
		t.Fatal("a runner error was swallowed")
	}
	if st := s.Status()[0]; st.LastError == "" || st.Running {
		t.Errorf("failure not recorded, or job left marked running: %+v", st)
	}
	if _, err := s.RunNow(context.Background(), "nope"); err == nil {
		t.Error("unknown job accepted")
	}
}

func TestBadExpressionIsAStartupError(t *testing.T) {
	if _, err := New([]Job{{Name: "j", Cron: "99 * * * *", Prompt: "x"}}, nil, nil); err == nil {
		t.Error("a schedule that could never fire was accepted")
	}
	if _, err := New([]Job{{Name: "j", Cron: "* * * * *"}}, nil, nil); err == nil {
		t.Error("a schedule with no prompt was accepted")
	}
}
