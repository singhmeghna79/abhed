package model

import "fmt"

func init() {
	Register("watsonx", "IBM watsonx.ai (project or space scoped)",
		func(s Spec) (Adapter, error) {
			if s.BaseURL == "" {
				return nil, fmt.Errorf("watsonx needs a base_url")
			}
			if s.Model == "" {
				return nil, fmt.Errorf("watsonx needs a model")
			}
			return NewWatsonX(WatsonXConfig{
				Defaults:  s.Params,
				BaseURL:   s.BaseURL,
				APIKey:    s.APIKey,
				ProjectID: s.Project,
				SpaceID:   s.Get("space_id"),
				ModelID:   s.Model,
				Version:   s.Get("api_version"),
				IAMURL:    s.Get("iam_url"),
				Profile: Profile{
					Name:            s.Model,
					ContextWindow:   s.ContextWindow,
					MaxOutputTokens: s.MaxOutputTokens,
					SupportsTools:   true,
					SupportsStream:  true,
					ToolCallFormat:  orElse(s.ToolCallFormat, "json"),
					ReasoningTokens: true,
					Sampling: Sampling{
						Temperature: true, TopP: true, Stop: true,
						FrequencyPenalty: true, PresencePenalty: true,
					},
				},
			}), nil
		})
}
