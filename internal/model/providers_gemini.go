package model

import "fmt"

func init() {
	Register("gemini", "Google Gemini (generativelanguage API)",
		func(s Spec) (Adapter, error) {
			if s.Model == "" {
				return nil, fmt.Errorf("provider %q needs a model", s.Type)
			}
			g := NewGemini(s.BaseURL, s.APIKey, s.Model, Profile{
				Name:            s.Model,
				ContextWindow:   s.ContextWindow,
				MaxOutputTokens: s.MaxOutputTokens,
				SupportsTools:   true,
				SupportsStream:  true,
				ToolCallFormat:  "json",
				ReasoningTokens: true,
				Sampling: Sampling{
					Temperature: true, TopP: true, TopK: true, Seed: true,
					Stop: true, Think: true, ThinkingBudget: true,
				},
			})
			g.Defaults = s.Params
			return g, nil
		})
}
