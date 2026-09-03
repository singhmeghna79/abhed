package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yuvrajsingh/titan/internal/tools"
)

// Tool loads a skill's instructions.
//
// The whole point of a tool rather than prompt inclusion is cost: the listing
// in the system prompt is a name and one line each, and the body — which may
// run to thousands of tokens — is paid only in the sessions that actually use
// it.
type Tool struct{ R *Registry }

func (Tool) Name() string { return "skill" }

// Mutates is false. Reading instructions changes nothing; whatever the
// instructions then tell the agent to do goes through the ordinary permission
// checks for those tools. Marking this as mutating would prompt the user to
// approve reading a file, which trains them to click through prompts.
func (Tool) Mutates() bool { return false }

func (t Tool) Description() string {
	names := ""
	if t.R != nil && t.R.Len() > 0 {
		names = " Available: " + strings.Join(t.R.Names(), ", ") + "."
	}
	return "Load the full instructions for a named skill." + names +
		" Call this when a request matches a skill listed in the system prompt, " +
		"then follow the instructions it returns."
}

func (Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "name":{"type":"string","description":"The skill to load, exactly as listed."}
  },
  "required":["name"]
}`)
}

type args struct {
	Name string `json:"name"`
}

func (t Tool) Run(_ context.Context, _ *tools.Session, raw json.RawMessage) tools.Result {
	var a args
	if err := json.Unmarshal(raw, &a); err != nil {
		return errf("Invalid arguments for skill: %v", err)
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		return errf("name is required.")
	}
	if t.R == nil || t.R.Len() == 0 {
		return errf("No skills are configured on this deployment.")
	}

	s, ok := t.R.Get(name)
	if !ok {
		// Naming the alternatives ends the retry loop that a bare "not found"
		// otherwise causes.
		return errf("No skill named %q. Available: %s.",
			name, strings.Join(t.R.Names(), ", "))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Skill: %s\n", s.Name)
	if s.Dir != "" {
		// Instructions routinely reference files beside them ("run the script
		// in scripts/check.sh"), and a relative path is meaningless without
		// this.
		fmt.Fprintf(&b, "Skill directory: %s\n", s.Dir)
	}
	b.WriteString("\n")
	b.WriteString(s.Body)
	return tools.Result{Content: b.String()}
}

func errf(format string, a ...any) tools.Result {
	return tools.Result{Content: fmt.Sprintf(format, a...), IsError: true}
}
