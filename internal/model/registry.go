package model

import (
	"fmt"
	"sort"
	"strings"
)

// Spec is the provider-neutral description of one configured endpoint.
//
// It exists so the factory for a provider takes a single argument that the
// config package can fill in without either package importing the other.
type Spec struct {
	Type            string
	BaseURL         string
	Model           string
	APIKey          string
	ContextWindow   int
	MaxOutputTokens int
	ToolCallFormat  string
	ReasoningTags   []string
	Params          Params

	// Region and Project scope a cloud-hosted deployment: an AWS region, a
	// Google project, an Azure resource. Which of them a provider needs is the
	// provider's business.
	Region  string
	Project string

	// Extra carries the settings only one provider understands — watsonx's
	// space_id and api_version, Azure's deployment name, Vertex's location.
	// Keeping them here rather than as fields on every Spec stops this struct
	// growing a column per vendor.
	Extra map[string]string
}

// Get reads an Extra value.
func (s Spec) Get(key string) string { return s.Extra[key] }

// Factory builds an adapter from a spec.
type Factory func(Spec) (Adapter, error)

type provider struct {
	name    string
	summary string
	build   Factory
}

var providers = map[string]provider{}

// Register adds a provider under a config "type". Called from each adapter's
// init, so the set of providers is the set of files compiled in — which is what
// makes an air-gapped build able to drop the cloud ones without editing a
// factory.
func Register(name, summary string, build Factory) {
	providers[name] = provider{name: name, summary: summary, build: build}
}

// New builds the adapter for a spec, validating its parameters against what the
// provider actually honours.
func New(s Spec) (Adapter, error) {
	p, ok := providers[s.Type]
	if !ok {
		return nil, fmt.Errorf("unknown provider type %q; known types are %s",
			s.Type, strings.Join(Providers(), ", "))
	}
	a, err := p.build(s)
	if err != nil {
		return nil, err
	}
	if err := s.Params.Validate(s.Type, a.Profile().Sampling); err != nil {
		return nil, err
	}
	return a, nil
}

// Providers lists the registered type names.
func Providers() []string {
	out := make([]string, 0, len(providers))
	for n := range providers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Describe lists each provider with its one-line summary, for `titan providers`.
func Describe() []string {
	out := make([]string, 0, len(providers))
	for _, n := range Providers() {
		out = append(out, fmt.Sprintf("%-20s %s", n, providers[n].summary))
	}
	return out
}

// Known reports whether a type is registered.
func Known(name string) bool {
	_, ok := providers[name]
	return ok
}

// splitList parses a comma-separated Extra value.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
