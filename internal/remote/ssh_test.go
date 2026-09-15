package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNewHostValidates(t *testing.T) {
	for _, c := range []HostConfig{
		{Addr: "h", User: "u"}, // no name
		{Name: "n", User: "u"}, // no addr
		{Name: "n", Addr: "h"}, // no user
	} {
		if _, err := NewHost(c); err == nil {
			t.Errorf("accepted incomplete host config %+v", c)
		}
	}
}

func TestNewHostDefaultsPort(t *testing.T) {
	h, err := NewHost(HostConfig{Name: "n", Addr: "example.com", User: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Addr() != "example.com:22" {
		t.Errorf("Addr() = %q, want the default port appended", h.Addr())
	}
}

// The model may only name a host the operator declared. Anything else must be
// refused with the list, so it stops guessing.
func TestToolRefusesUndeclaredHost(t *testing.T) {
	reg, errs := NewRegistry([]HostConfig{{Name: "web1", Addr: "h", User: "u"}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	args, _ := json.Marshal(map[string]string{"host": "prod-db", "command": "ls"})
	res := Tool{R: reg}.Run(context.TODO(), nil, args)
	if !res.IsError {
		t.Fatal("connected to a host that was never declared")
	}
	if !strings.Contains(res.Content, "web1") {
		t.Errorf("refusal does not list the configured hosts: %s", res.Content)
	}
}

func TestToolWithNoHostsSaysSo(t *testing.T) {
	reg, _ := NewRegistry(nil)
	args, _ := json.Marshal(map[string]string{"host": "x", "command": "ls"})
	res := Tool{R: reg}.Run(context.TODO(), nil, args)
	if !res.IsError || !strings.Contains(res.Content, "operator") {
		t.Errorf("unhelpful message with no hosts: %s", res.Content)
	}
}

// A remote command runs outside the sandbox with no undo, so it must always
// require approval — there is no read-only classification to be had.
func TestToolAlwaysMutates(t *testing.T) {
	if !(Tool{}).Mutates() {
		t.Error("ssh does not declare itself mutating, so it could be auto-approved")
	}
}

func TestRegistrySkipsBadHostsButKeepsGood(t *testing.T) {
	reg, errs := NewRegistry([]HostConfig{
		{Name: "good", Addr: "h", User: "u"},
		{Name: "", Addr: "h", User: "u"}, // invalid
	})
	if len(errs) != 1 {
		t.Errorf("errs = %v, want exactly one", errs)
	}
	if reg.Len() != 1 {
		t.Errorf("Len() = %d, want the valid host to survive", reg.Len())
	}
}

// A remote command that prints a gigabyte must not take the agent down.
func TestLimitedWriterCaps(t *testing.T) {
	var buf bytes.Buffer
	w := &limitedWriter{w: &buf, limit: 10}
	n, err := w.Write(bytes.Repeat([]byte("x"), 100))
	if err != nil {
		t.Fatalf("write returned an error, which would fail the command: %v", err)
	}
	if n != 100 {
		t.Errorf("Write reported %d, want the full length so the caller does not retry", n)
	}
	if buf.Len() != 10 {
		t.Errorf("buffered %d bytes, want the limit of 10", buf.Len())
	}
	if !w.cut {
		t.Error("truncation not recorded")
	}
}

func TestExpandHome(t *testing.T) {
	got := expandHome("~/.ssh/id_ed25519")
	if strings.HasPrefix(got, "~") {
		t.Errorf("expandHome left the tilde: %q", got)
	}
	if got := expandHome("/abs/path"); got != "/abs/path" {
		t.Errorf("expandHome changed an absolute path: %q", got)
	}
}
