package model

import "fmt"

func init() {
	Register("anthropic", "Anthropic Messages API (api.anthropic.com)",
		func(s Spec) (Adapter, error) {
			if s.Model == "" {
				return nil, fmt.Errorf("provider %q needs a model", s.Type)
			}
			a := NewAnthropic(s.BaseURL, s.APIKey, s.Model, Profile{
				Name:            s.Model,
				ContextWindow:   s.ContextWindow,
				MaxOutputTokens: s.MaxOutputTokens,
				SupportsTools:   true,
				SupportsStream:  true,
				ToolCallFormat:  "json",
				ReasoningTokens: true,
				// Explicit cache_control on the system block, which is what
				// makes the stable prefix a cache read rather than a re-bill.
				CachePrefix: true,
				Sampling: Sampling{
					Temperature: true, TopP: true, TopK: true, Stop: true,
					Think: true, ThinkingBudget: true, Effort: true,
				},
			})
			a.Defaults = s.Params
			if v := s.Get("api_version"); v != "" {
				a.Version = v
			}
			if b := s.Get("beta"); b != "" {
				a.Beta = splitList(b)
			}
			return a, nil
		})
}
