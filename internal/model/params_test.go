package model

import "testing"

func ptr[T any](v T) *T { return &v }

func TestValidateRejectsUnsupportedParams(t *testing.T) {
	// min_p is a local-server knob. Sending it to a hosted API is silently
	// ignored there, which is the failure this check exists to prevent.
	err := Params{MinP: ptr(0.05)}.Validate("openai", SamplingOpenAI())
	if err == nil {
		t.Fatal("want an error for min_p on a hosted provider")
	}
	if got := err.Error(); !contains(got, "min_p") || !contains(got, "openai") {
		t.Errorf("error should name the knob and the provider, got %q", got)
	}
	if err := (Params{MinP: ptr(0.05)}).Validate("vllm", SamplingLocal()); err != nil {
		t.Errorf("min_p is supported locally: %v", err)
	}
}

func TestValidateRangeChecks(t *testing.T) {
	cases := []struct {
		name string
		p    Params
		ok   bool
	}{
		{"temperature in range", Params{Temperature: ptr(0.7)}, true},
		{"temperature at zero is legal", Params{Temperature: ptr(0.0)}, true},
		{"temperature too high", Params{Temperature: ptr(2.5)}, false},
		{"temperature negative", Params{Temperature: ptr(-0.1)}, false},
		{"top_p in range", Params{TopP: ptr(0.9)}, true},
		{"top_p above one", Params{TopP: ptr(1.5)}, false},
		{"frequency penalty in range", Params{FrequencyPenalty: ptr(-1.0)}, true},
		{"frequency penalty out of range", Params{FrequencyPenalty: ptr(3.0)}, false},
		{"effort valid", Params{Effort: EffortHigh}, true},
		{"effort nonsense", Params{Effort: "maximum"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.p.Validate("vllm", SamplingLocal())
			if (err == nil) != c.ok {
				t.Fatalf("Validate() error = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

// An unset parameter must stay distinguishable from one set to zero: the first
// means "leave the model's default alone", the second means greedy decoding.
func TestUnsetIsNotZero(t *testing.T) {
	var unset Params
	if unset.Temperature != nil {
		t.Fatal("a zero Params must have no temperature set")
	}
	zero := Params{Temperature: ptr(0.0)}
	if zero.Temperature == nil || *zero.Temperature != 0 {
		t.Fatal("an explicit zero temperature must survive")
	}
}

func TestMergeOverlaysOnlySetFields(t *testing.T) {
	base := Params{Temperature: ptr(0.7), TopP: ptr(0.9), MaxTokens: 4096}
	got := base.Merge(Params{Temperature: ptr(0.2)})
	if *got.Temperature != 0.2 {
		t.Errorf("temperature = %v, want the override", *got.Temperature)
	}
	if got.TopP == nil || *got.TopP != 0.9 {
		t.Error("top_p should survive a merge that does not mention it")
	}
	if got.MaxTokens != 4096 {
		t.Error("max_tokens should survive a merge that does not mention it")
	}
}

// The request-level fields override the configured defaults, so a caller can
// pin one knob without restating the rest.
func TestRequestSamplingPrefersRequestFields(t *testing.T) {
	req := Request{
		Params:      Params{Temperature: ptr(0.7), TopP: ptr(0.9), MaxTokens: 100},
		Temperature: ptr(0.1),
		MaxTokens:   200,
	}
	got := req.Sampling()
	if *got.Temperature != 0.1 {
		t.Errorf("temperature = %v, want 0.1", *got.Temperature)
	}
	if got.MaxTokens != 200 {
		t.Errorf("max_tokens = %d, want 200", got.MaxTokens)
	}
	if got.TopP == nil || *got.TopP != 0.9 {
		t.Error("top_p should come from Params when the request does not set it")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
