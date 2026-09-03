// Package skills loads reusable instruction sets the agent can invoke.
//
// A skill is a folder with a SKILL.md: frontmatter naming it and describing
// when to use it, then a body of instructions. It is the mechanism for
// capturing procedural knowledge that does not belong in code — how this team
// cuts a release, the checklist for a schema migration, the house style for an
// incident report.
//
// The load-bearing design decision is that only the NAME and DESCRIPTION of
// each skill sit in the system prompt; the body is fetched by a tool call when
// the agent decides the skill applies. Putting every body in the prompt would
// be simpler and quietly ruinous: twenty skills of a thousand tokens each is
// 20k tokens paid on every request of every session, whether or not any of
// them is relevant. The listing costs about fifteen tokens per skill and the
// body is paid only when used.
//
// Skills are also the most direct route for untrusted instructions to reach
// the model, since a skill body IS instructions by construction. So they come
// only from directories the operator configured — never from the workspace the
// agent is editing, which would let a repository carry its own instructions to
// the agent reading it.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Skill is one loaded instruction set.
type Skill struct {
	// Name is how the agent invokes it. Kebab-case, unique.
	Name string
	// Description tells the model WHEN to use this skill. It is the only part
	// besides the name that reaches the prompt, so a vague description means a
	// skill that is never invoked or invoked for everything.
	Description string
	// Body is the instruction text, loaded on demand.
	Body string
	// Dir is the skill's directory, so its instructions can reference files
	// beside them.
	Dir string
	// Source is the configured root it came from, for diagnostics.
	Source string
}

// Registry holds the discovered skills.
type Registry struct {
	mu     sync.RWMutex
	byName map[string]*Skill
	order  []string
}

func NewRegistry() *Registry {
	return &Registry{byName: map[string]*Skill{}}
}

// Load discovers skills under each root. Later roots win on a name collision,
// so a project can override a team-wide skill deliberately.
func Load(roots []string) (*Registry, []error) {
	r := NewRegistry()
	var errs []error
	for _, root := range roots {
		expanded := expandHome(root)
		found, err := discover(expanded)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, s := range found {
			s.Source = expanded
			r.add(s)
		}
	}
	return r, errs
}

func (r *Registry) add(s *Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byName[s.Name]; !exists {
		r.order = append(r.order, s.Name)
	}
	r.byName[s.Name] = s
}

func (r *Registry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.byName[name]
	return s, ok
}

func (r *Registry) All() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Skill, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.byName[n])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byName)
}

func (r *Registry) Names() []string {
	out := make([]string, 0, r.Len())
	for _, s := range r.All() {
		out = append(out, s.Name)
	}
	return out
}

// Listing renders the prompt section: names and descriptions only.
//
// This is the whole economy of the design. It is deliberately compact, and it
// tells the model the one thing it needs — that the body is fetched by calling
// a tool, not guessed at.
func (r *Registry) Listing() string {
	all := r.All()
	if len(all) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Skills\n")
	b.WriteString("Procedures this deployment has defined. When a request matches one, " +
		"call the `skill` tool with its name to read the full instructions, then follow them. " +
		"Do not guess at a skill's contents from its description.\n\n")
	for _, s := range all {
		fmt.Fprintf(&b, "- `%s` — %s\n", s.Name, s.Description)
	}
	return b.String()
}

// ---------------------------------------------------------------- discovery

const skillFile = "SKILL.md"

// discover walks a root looking for SKILL.md files. A missing root is not an
// error: configuring a directory that does not exist yet is a reasonable thing
// to do before writing the first skill.
func discover(root string) ([]*Skill, error) {
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skills: read %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("skills: %s is not a directory", root)
	}

	var out []*Skill
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("skills: read %s: %w", root, err)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		path := filepath.Join(dir, skillFile)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue // a directory without SKILL.md is not a skill
		}
		if err != nil {
			return out, fmt.Errorf("skills: read %s: %w", path, err)
		}
		s, err := Parse(string(data))
		if err != nil {
			return out, fmt.Errorf("skills: %s: %w", path, err)
		}
		if s.Name == "" {
			s.Name = e.Name()
		}
		s.Dir = dir
		out = append(out, s)
	}
	return out, nil
}

// Parse reads a SKILL.md: YAML-ish frontmatter between --- markers, then the
// body.
//
// The frontmatter is name/description/allowed-tools and nothing else, so it is
// parsed directly rather than through a YAML library — the same reasoning as
// the kubeconfig reader: a narrow known shape does not justify a large
// dependency in an air-gapped bundle.
func Parse(content string) (*Skill, error) {
	s := &Skill{}
	text := strings.ReplaceAll(content, "\r\n", "\n")

	if !strings.HasPrefix(strings.TrimLeft(text, " \t\n"), "---") {
		return nil, fmt.Errorf("missing frontmatter: a SKILL.md starts with a --- block " +
			"containing name and description")
	}
	text = strings.TrimLeft(text, " \t\n")
	rest := text[3:]
	if i := strings.Index(rest, "\n---"); i >= 0 {
		frontmatter := rest[:i]
		s.Body = strings.TrimSpace(rest[i+4:])
		parseFrontmatter(frontmatter, s)
	} else {
		return nil, fmt.Errorf("frontmatter is not closed with ---")
	}

	if s.Description == "" {
		// Without a description the model has no basis to choose this skill,
		// so it will either never invoke it or invoke it for everything.
		return nil, fmt.Errorf("frontmatter needs a description saying WHEN to use this skill")
	}
	if strings.TrimSpace(s.Body) == "" {
		return nil, fmt.Errorf("skill has no instructions after the frontmatter")
	}
	return s, nil
}

func parseFrontmatter(fm string, s *Skill) {
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			s.Name = value
		case "description":
			s.Description = value
		}
	}
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
