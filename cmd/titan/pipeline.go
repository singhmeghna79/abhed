package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/yuvrajsingh/titan/internal/agent"
	"github.com/yuvrajsingh/titan/internal/model"
	"github.com/yuvrajsingh/titan/internal/pipeline"
	"github.com/yuvrajsingh/titan/internal/skills"
	"github.com/yuvrajsingh/titan/internal/tools"
)

// pipelineRunner builds the function the skill tool calls to execute a
// declared pipeline.
//
// Everything a step does goes through the same machinery an ordinary turn uses:
// a tool step is dispatched through the registry and the policy engine, and a
// model step is an ordinary completion on the configured adapter. The pipeline
// decides what happens and in what order; it does not decide what is permitted.
func pipelineRunner(
	adapter model.Adapter,
	registry *tools.Registry,
	session *tools.Session,
	loop *agent.LoopHolder,
) func(context.Context, *skills.Skill, string) (string, error) {

	return func(ctx context.Context, s *skills.Skill, input string) (string, error) {
		var p pipeline.Pipeline
		if err := json.Unmarshal(s.Pipeline, &p); err != nil {
			return "", fmt.Errorf("pipeline.json: %w", err)
		}
		if err := p.Validate(); err != nil {
			return "", err
		}

		runner := &pipeline.Runner{
			Tool: func(ctx context.Context, name string, args json.RawMessage) (string, error) {
				tool, found := registry.Get(name)
				if !found {
					return "", fmt.Errorf("no tool named %q", name)
				}
				res := tool.Run(ctx, session, args)
				if res.IsError {
					return "", fmt.Errorf("%s", res.Content)
				}
				return res.Content, nil
			},
			Model: func(ctx context.Context, prompt string, schema json.RawMessage) (string, error) {
				return completeOnce(ctx, adapter, prompt, schema)
			},
			// Each stage is recorded, so the decomposition, the sufficiency
			// verdict and the reason for every extra hop are visible in the
			// transcript rather than being inferred from tool calls.
			Event: func(stage, detail string, data map[string]any) {
				loop.RecordPipelineStage(s.Name, stage, detail, data)
			},
		}

		res, err := runner.Run(ctx, p, input)
		if err != nil {
			return "", err
		}
		return renderForModel(s, res), nil
	}
}

// completeOnce asks the model for one answer, with no tools offered.
//
// A pipeline's model steps classify, reformulate and judge; giving them the
// tool set would invite them to call something instead of answering, which is
// the failure the pipeline exists to remove.
func completeOnce(ctx context.Context, adapter model.Adapter, prompt string, schema json.RawMessage) (string, error) {
	system := "You answer exactly what is asked, with no preamble."
	if len(schema) > 0 {
		system += " Reply with JSON matching this schema and nothing else:\n" + string(schema)
	}
	stream, err := adapter.Complete(ctx, model.Request{
		System:    system,
		Messages:  []model.Message{{Role: model.RoleUser, Content: prompt}},
		MaxTokens: 2048,
	})
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for chunk := range stream {
		switch chunk.Type {
		case model.ChunkText:
			out.WriteString(chunk.Text)
		case model.ChunkError:
			return "", chunk.Err
		}
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", fmt.Errorf("the model returned nothing")
	}
	return out.String(), nil
}

// renderForModel turns what the pipeline gathered into the context the model
// writes its answer from.
//
// The skill body still comes along: the pipeline guarantees the gathering
// happened, and the instructions say how to present it — citation rules, the
// mode to write in, what never to mention. Those are judgements, which is what
// the model is for.
func renderForModel(s *skills.Skill, res *pipeline.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The %s pipeline ran and gathered the following.\n\n", s.Name)

	for key, val := range res.State.All() {
		if key == "input" {
			continue
		}
		fmt.Fprintf(&b, "## %s\n%s\n\n", key, format(val))
	}
	if len(res.Failures) > 0 {
		b.WriteString("## steps that did not complete\n")
		for _, f := range res.Failures {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		b.WriteString("\nSay so if this changes what you can answer.\n\n")
	}
	b.WriteString("---\n\nNow follow these instructions to write the answer. " +
		"The gathering above has already happened; do not repeat it.\n\n")
	b.WriteString(s.Body)
	return b.String()
}

func format(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}

// lastPrompt returns the request the current turn is answering.
//
// A pipeline needs it and the skill tool's arguments do not carry it: the model
// calls the skill by name, not by repeating the question. Holding it here keeps
// the tool's schema unchanged, so a skill invocation still looks the same to
// the model.
var currentPrompt struct {
	mu sync.Mutex
	s  string
}

func setPrompt(p string) {
	currentPrompt.mu.Lock()
	currentPrompt.s = p
	currentPrompt.mu.Unlock()
}

func lastPrompt() string {
	currentPrompt.mu.Lock()
	defer currentPrompt.mu.Unlock()
	return currentPrompt.s
}
