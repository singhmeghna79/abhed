package model

import "fmt"

func init() {
	Register("gemini", "Google Gemini (generativelanguage API)", geminiStyle(""))
}

// geminiStyle builds the factory for a Gemini-compatible endpoint, so a custom
// provider defined in configuration reuses exactly the built-in path.
func geminiStyle(defaultURL string) Factory {
	return func(s Spec) (Adapter, error) {
		if s.Model == "" {
			return nil, fmt.Errorf("provider %q needs a model", s.Type)
		}
		base := s.BaseURL
		if base == "" {
			base = defaultURL
		}
		g := NewGemini(base, s.APIKey, s.Model, Profile{
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
	}
}
