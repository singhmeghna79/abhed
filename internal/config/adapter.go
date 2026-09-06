package config

import "github.com/yuvrajsingh/titan/internal/model"

// Spec converts a provider config into the model package's neutral form.
//
// This lives in config rather than model so that model stays free of any
// knowledge of the file format, and config stays free of any knowledge of how
// a given provider is built. The registry in between is what lets a new
// provider be one new file with an init, rather than an edit to a factory
// switch in main.
func (p ProviderConfig) Spec() model.Spec {
	extra := map[string]string{}
	for k, v := range p.Extra {
		extra[k] = v
	}
	// The watsonx fields predate Extra and stay first-class in the config file;
	// carry them through so one provider's settings do not become a special
	// case in the model package.
	if p.SpaceID != "" {
		extra["space_id"] = p.SpaceID
	}
	if p.APIVersion != "" {
		extra["api_version"] = p.APIVersion
	}
	if p.IAMURL != "" {
		extra["iam_url"] = p.IAMURL
	}

	return model.Spec{
		Type:            p.Type,
		BaseURL:         p.BaseURL,
		Model:           p.Model,
		APIKey:          p.APIKey,
		ContextWindow:   p.ContextWindow,
		MaxOutputTokens: p.MaxOutputTokens,
		ToolCallFormat:  p.ToolCallFormat,
		ReasoningTags:   p.ReasoningTags,
		Region:          p.Region,
		Project:         p.ProjectID,
		Params:          p.Params.Model(p.Think),
		Extra:           extra,
	}
}

// Model converts the config form of the sampling parameters.
//
// think is the older top-level provider field, kept working: an explicit
// params.think wins, and the legacy field applies when params does not set it.
func (c ParamsConfig) Model(think *bool) model.Params {
	out := model.Params{
		Temperature:       c.Temperature,
		TopP:              c.TopP,
		TopK:              c.TopK,
		MinP:              c.MinP,
		RepetitionPenalty: c.RepetitionPenalty,
		FrequencyPenalty:  c.FrequencyPenalty,
		PresencePenalty:   c.PresencePenalty,
		Seed:              c.Seed,
		MaxTokens:         c.MaxTokens,
		Stop:              c.Stop,
		Effort:            model.EffortLevel(c.Effort),
		Think:             c.Think,
		ThinkingBudget:    c.ThinkingBudget,
	}
	if out.Think == nil {
		out.Think = think
	}
	return out
}

// Adapter builds the model adapter this provider describes, validating its
// parameters against what the provider actually honours.
func (p ProviderConfig) Adapter() (model.Adapter, error) {
	return model.New(p.Spec())
}
