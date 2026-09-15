package app

import (
	"context"
	"fmt"
	"os"

	"github.com/zybuu-ai/abhed/ee/schedule"
	ee "github.com/zybuu-ai/abhed/ee/server"
	"github.com/zybuu-ai/abhed/app"
	"github.com/zybuu-ai/abhed/config"
	"github.com/zybuu-ai/abhed/server"
)

// Schedules runs the config's schedules against the server: prompts started
// by the clock, each an ordinary session that the admin routes can list and
// fire by hand.
func Schedules() app.Option {
	return func(a *app.App) {
		app.WithFeature("schedules")(a)
		app.OnServe(startSchedules)(a)
	}
}

// startSchedules parses every schedule before anything runs, so a bad
// expression is a startup error, not a job that never fires. The scheduler
// needs the server to start runs and the server's admin view needs the
// scheduler to list them, so the runner is bound after construction and the
// routes mounted on the server in hand.
func startSchedules(ctx context.Context, s *server.Server, cfg config.Config) (func(), error) {
	if len(cfg.Schedules) == 0 {
		return nil, nil
	}
	jobs := make([]schedule.Job, 0, len(cfg.Schedules))
	for _, sc := range cfg.Schedules {
		jobs = append(jobs, schedule.Job{Name: sc.Name, Cron: sc.Cron, Prompt: sc.Prompt,
			Mode: sc.Mode, Provider: sc.Provider, Disabled: sc.Disabled})
	}
	sched, err := schedule.New(jobs, nil, s.Logger())
	if err != nil {
		return nil, err
	}
	sched.Bind(ee.ScheduleRunner(s, sched))
	s.Mount(ee.SchedulesMount(sched))
	sched.Start(ctx)
	fmt.Fprintf(os.Stderr, "  schedules %d job(s)\n", len(cfg.Schedules))
	return nil, nil
}
