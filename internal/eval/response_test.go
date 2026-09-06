package eval

import "testing"

// The retrieval corpus is graded on the agent's prose, not on files it wrote.
// These are the exact checks the Paver zrag benchmark applies, so a change that
// breaks them would silently change what the comparison measures.
func TestResponseMatchesAssertion(t *testing.T) {
	cases := []struct {
		name     string
		a        Assertion
		response string
		wantPass bool
	}{
		{"citation present", Assertion{Type: "response_matches", Value: `\[\s*\d+`},
			"A sysplex is a cluster [1] of systems.", true},
		{"citation absent", Assertion{Type: "response_matches", Value: `\[\s*\d+`},
			"A sysplex is a cluster of systems.", false},
		{"grouped citation counts", Assertion{Type: "response_matches", Value: `\[\s*\d+`},
			"A sysplex is a cluster [1, 3, 14] of systems.", true},

		{"leaked run.sh is a failure", Assertion{Type: "response_matches",
			Value: `(?i)run\.sh|zrag_client|` + "```" + `bash", `, Negate: true},
			"Here is the answer.", true},
		{"clean answer passes the leak check", Assertion{Type: "response_matches",
			Value: `(?i)run\.sh|zrag_client`, Negate: true},
			"A sysplex is a cluster [1].", true},
		{"mentioning run.sh fails", Assertion{Type: "response_matches",
			Value: `(?i)run\.sh|zrag_client`, Negate: true},
			"Run bash $SKILL_DIR/scripts/run.sh retrieve ...", false},

		{"error text fails", Assertion{Type: "response_matches",
			Value: `(?i)traceback|stack trace|\bexception\b`, Negate: true},
			"Traceback (most recent call last):", false},
		{"empty response fails the non-empty check", Assertion{Type: "response_matches", Value: `\S`},
			"   \n  ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.a.check(t.TempDir(), nil, c.response)
			if (err == nil) != c.wantPass {
				t.Fatalf("check() error = %v, want pass=%v", err, c.wantPass)
			}
		})
	}
}

// A malformed pattern must fail loudly rather than silently passing every task.
func TestResponseMatchesRejectsBadRegexp(t *testing.T) {
	a := Assertion{Type: "response_matches", Value: `([unclosed`}
	if err := a.check(t.TempDir(), nil, "anything"); err == nil {
		t.Fatal("an invalid regexp must be reported, not treated as a pass")
	}
}
