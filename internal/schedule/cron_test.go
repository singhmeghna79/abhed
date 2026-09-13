package schedule

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNextFiringTimes(t *testing.T) {
	cases := []struct{ expr, from, want string }{
		{"0 9 * * 1-5", "2026-09-11 10:00", "2026-09-14 09:00"}, // Fri after nine -> Mon
		{"*/15 * * * *", "2026-09-13 10:07", "2026-09-13 10:15"},
		{"30 2 1 * *", "2026-09-13 10:00", "2026-10-01 02:30"},
		{"@hourly", "2026-09-13 10:07", "2026-09-13 11:00"},
		{"0 0 29 2 *", "2026-03-01 00:00", "2028-02-29 00:00"}, // leap day
		{"5/20 * * * *", "2026-09-13 10:00", "2026-09-13 10:05"},
		{"0 12 15 * 3", "2026-09-13 10:00", "2026-09-15 12:00"}, // dom OR dow: the 15th (Tue)
	}
	for _, c := range cases {
		e, err := Parse(c.expr)
		if err != nil {
			t.Errorf("%q: %v", c.expr, err)
			continue
		}
		if got := e.Next(at(c.from)); got.Format("2006-01-02 15:04") != c.want {
			t.Errorf("%q from %s: got %s, want %s", c.expr, c.from, got.Format("2006-01-02 15:04"), c.want)
		}
	}
}

func TestRejectsWhatItWouldMisread(t *testing.T) {
	for _, bad := range []string{"", "0 9 * *", "60 * * * *", "0 25 * * *", "0 0 0 * *",
		"0 0 * 13 *", "a * * * *", "*/0 * * * *", "10-5 * * * *"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q parsed; it should be refused", bad)
		}
	}
}

func TestNeverFiringReturnsZero(t *testing.T) {
	e, _ := Parse("0 0 31 2 *") // 31 February
	if !e.Next(at("2026-01-01 00:00")).IsZero() {
		t.Error("31 February found a firing time")
	}
}
