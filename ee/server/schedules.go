package server

import (
	"context"
	"net/http"

	"github.com/zybuu-ai/abhed/ee/schedule"
	"github.com/zybuu-ai/abhed/server"
)

// Schedules run prompts on a timetable. The scheduler owns the clock and the
// no-overlap rule; the server owns starting the runs. Neither is wired into
// the other: the runner below binds them, and the mount exposes the result,
// so a deployment without schedules carries no scheduler and no routes for
// one.

// ScheduleRunner is the scheduler's Runner for this server. It returns once
// the session is running, and tells the scheduler when the run ends so the
// job may fire again.
//
// A scheduled run has nobody to ask, so it is unattended: an "ask" under the
// configured mode becomes a denial, recorded like any other. Operators who
// want a scheduled job to edit files give it a mode or allow rules that do
// not need a person — that is a choice made in config, on the record, not a
// default made here.
func ScheduleRunner(s *server.Server, sched *schedule.Scheduler) schedule.Runner {
	return func(ctx context.Context, job schedule.Job) (string, error) {
		return s.StartSession(ctx, server.StartSpec{
			Prompt:     job.Prompt,
			Mode:       job.Mode,
			Provider:   job.Provider,
			User:       "schedule:" + job.Name,
			Unattended: true,
			OnEnd:      func(string, error) { sched.Finished(job.Name) },
		})
	}
}

// SchedulesMount registers the admin view of a scheduler: what is due, what
// last ran, and a "run it now" button.
func SchedulesMount(sched *schedule.Scheduler) server.Mount {
	return func(s *server.Server, mux *http.ServeMux) {
		mux.Handle("GET /v1/admin/schedules", s.Admin(func(w http.ResponseWriter, r *http.Request) {
			server.WriteJSON(w, http.StatusOK, map[string]any{"schedules": sched.Status()})
		}))
		mux.Handle("POST /v1/admin/schedules/{name}/run", s.Admin(func(w http.ResponseWriter, r *http.Request) {
			id, err := sched.RunNow(r.Context(), r.PathValue("name"))
			if err != nil {
				server.WriteJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			s.Logger().Info("schedule run by admin", "job", r.PathValue("name"),
				"by", server.UserOf(r.Context()), "session", id)
			server.WriteJSON(w, http.StatusAccepted, map[string]string{"session_id": id})
		}))
	}
}
