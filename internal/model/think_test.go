package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// Think must be omitted entirely when unset, not sent as false. A server that
// defaults to thinking on would otherwise have it silently disabled by every
// Abhed request, which is a behaviour change nobody asked for.
func TestThinkOmittedWhenUnset(t *testing.T) {
	c := NewOpenAICompatible("http://x/v1", "", "m", Profile{})
	body, err := json.Marshal(c.buildRequest(Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); strings.Contains(got, `"think"`) {
		t.Errorf("think sent when unset: %s", got)
	}
}

func TestThinkSentWhenSet(t *testing.T) {
	for _, want := range []bool{true, false} {
		c := NewOpenAICompatible("http://x/v1", "", "m", Profile{})
		c.Think = &want
		body, err := json.Marshal(c.buildRequest(Request{
			Messages: []Message{{Role: RoleUser, Content: "hi"}},
		}))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		v, found := got["think"]
		if !found {
			t.Fatalf("think not sent when set to %v", want)
		}
		if v != want {
			t.Errorf("think = %v, want %v", v, want)
		}
	}
}
