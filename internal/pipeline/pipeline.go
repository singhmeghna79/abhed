// Package pipeline runs the stages a skill declares, rather than asking a model
// to remember them.
//
// A skill is prose, and prose is followed unreliably. Measured on a 26B model
// against a seven-step retrieval skill: it wrote a correct plan — classify,
// decompose into two sub-questions, retrieve both in parallel, judge
// sufficiency — ending with "I will now call retrieve for both", and then
// called web search instead and never retrieved at all. Nothing was broken. It
// simply did not do what it had just decided to do.
//
// Prompt engineering does not fix that; it is the same mechanism failing. Nor
// does building one retrieval engine in Go, which would fix one skill and leave
// the next author with the same problem. What is missing is a way for a skill
// to say *these steps must happen, in this order, with these parts concurrent*
// — and have the harness be the thing that guarantees it.
//
// So a pipeline is declared in the skill's own frontmatter and executed here.
// The model still does what models are good at: classifying, reformulating,
// judging sufficiency, writing the answer. It no longer has to remember to.
package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Pipeline is a skill's declared sequence of stages.
type Pipeline struct {
	// Stages run in order. Steps within a stage run concurrently.
	Stages []Stage `json:"stages"`
	// MaxIterations bounds any stage that loops back, so a sufficiency check
	// that never returns true cannot spend the session.
	MaxIterations int `json:"max_iterations,omitempty"`
}

// Stage is one step or a set of steps that run together.
type Stage struct {
	Name string `json:"name"`
	// Steps in a stage run concurrently. One step is the common case.
	Steps []Step `json:"steps"`
	// When is a condition on a previous step's output, e.g.
	// "classify.complexity == complex". An unmet condition skips the stage.
	When string `json:"when,omitempty"`
	// Gate makes this stage a decision point: the model is asked a question,
	// and its answer either continues or jumps back to Repeat.
	Gate *Gate `json:"gate,omitempty"`
}

// Step is a single unit of work.
type Step struct {
	// Kind is "model" or "tool".
	Kind string `json:"kind"`
	// Tool names the tool for a tool step.
	Tool string `json:"tool,omitempty"`
	// Args is the tool's arguments, with {{...}} references to earlier output.
	Args json.RawMessage `json:"args,omitempty"`
	// Prompt instructs a model step. Also templated.
	Prompt string `json:"prompt,omitempty"`
	// Output names where this step's result is stored, for later reference.
	Output string `json:"output,omitempty"`
	// ForEach fans the step out over a list produced earlier, running one
	// instance per element — concurrently, since they do not depend on
	// each other. This is what makes "retrieve for each sub-question" a
	// property of the pipeline rather than a hope about the model.
	ForEach string `json:"for_each,omitempty"`
	// Required decides what a failure means. A required step that fails stops
	// the pipeline; a best-effort one records the failure and continues, which
	// is right for a web search that is nice to have and wrong for the
	// retrieval the answer depends on.
	Required bool `json:"required,omitempty"`
	// Schema, on a model step, asks for JSON matching this shape. Without it
	// the step returns prose.
	Schema json.RawMessage `json:"schema,omitempty"`
	// Timeout bounds one step.
	Timeout time.Duration `json:"-"`
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

// Gate turns a stage into a decision point.
type Gate struct {
	// Ask is the question put to the model, templated like a prompt.
	Ask string `json:"ask"`
	// Repeat names an earlier stage to return to when the gate is not
	// satisfied. Empty means continue regardless, which makes the gate a
	// recorded judgement rather than a loop.
	Repeat string `json:"repeat,omitempty"`
	// Output stores the gate's verdict and reasoning, so a reader can see why
	// another hop happened rather than inferring it.
	Output string `json:"output,omitempty"`
}

// Validate reports what is wrong with a declared pipeline.
//
// Checking at load rather than at run matters: a pipeline is a promise the
// harness makes on the author's behalf, and one that fails halfway through has
// already skipped the guarantees the author was relying on.
func (p Pipeline) Validate() error {
	if len(p.Stages) == 0 {
		return fmt.Errorf("pipeline has no stages")
	}
	seen := map[string]bool{}
	outputs := map[string]bool{}

	for i, st := range p.Stages {
		if st.Name == "" {
			return fmt.Errorf("stage %d has no name", i+1)
		}
		if seen[st.Name] {
			return fmt.Errorf("two stages are named %q; a gate could not say "+
				"which one to repeat", st.Name)
		}
		seen[st.Name] = true

		if len(st.Steps) == 0 && st.Gate == nil {
			return fmt.Errorf("stage %q has neither steps nor a gate", st.Name)
		}
		for j, step := range st.Steps {
			if err := step.validate(st.Name, j, outputs); err != nil {
				return err
			}
			if step.Output != "" {
				outputs[step.Output] = true
			}
		}
		if st.Gate != nil {
			if strings.TrimSpace(st.Gate.Ask) == "" {
				return fmt.Errorf("stage %q has a gate with no question", st.Name)
			}
			// A gate may only repeat a stage that has already run, or the
			// pipeline could jump forward into stages whose inputs do not
			// exist yet.
			if r := st.Gate.Repeat; r != "" && !seen[r] {
				return fmt.Errorf("stage %q repeats %q, which is not an earlier stage",
					st.Name, r)
			}
			if st.Gate.Output != "" {
				outputs[st.Gate.Output] = true
			}
		}
	}
	return nil
}

func (s Step) validate(stage string, idx int, outputs map[string]bool) error {
	where := fmt.Sprintf("stage %q step %d", stage, idx+1)
	switch s.Kind {
	case "tool":
		if s.Tool == "" {
			return fmt.Errorf("%s is a tool step with no tool", where)
		}
	case "model":
		if strings.TrimSpace(s.Prompt) == "" {
			return fmt.Errorf("%s is a model step with no prompt", where)
		}
	default:
		return fmt.Errorf("%s has kind %q; want \"tool\" or \"model\"", where, s.Kind)
	}
	// A fan-out over something that was never produced would silently run
	// zero times, which looks like a step that did nothing rather than a
	// mistake in the pipeline.
	if s.ForEach != "" && !outputs[rootOf(s.ForEach)] {
		return fmt.Errorf("%s fans out over %q, which no earlier step produces",
			where, s.ForEach)
	}
	return nil
}

// rootOf takes the first segment of a dotted reference, which is the name an
// earlier step stored.
func rootOf(ref string) string {
	if i := strings.IndexByte(ref, '.'); i >= 0 {
		return ref[:i]
	}
	return ref
}

// Timeout returns the step's timeout, or a default.
func (s Step) TimeoutOr(d time.Duration) time.Duration {
	if s.TimeoutMS > 0 {
		return time.Duration(s.TimeoutMS) * time.Millisecond
	}
	if s.Timeout > 0 {
		return s.Timeout
	}
	return d
}
