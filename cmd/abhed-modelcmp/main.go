// Command abhed-modelcmp compares candidate models on the two things Abhed
// actually needs from one, which are not the same thing and are not measured
// by the same benchmark.
//
// The first is structured tool calling. This is a hard gate: a model that
// describes a tool call in prose instead of emitting one cannot drive the
// agent loop at all, however well it writes. `abhed doctor` already checks
// this with a single trivial call; this runs a harder set, including a call
// that must be chosen from several tools and one that must be declined.
//
// The second is explanation quality. A coding-tuned model can pass every tool
// test and still answer "explain LLMs simply" with a textbook contents page —
// which is what prompted this comparison. Prose cannot be scored automatically
// without another model's opinion, so this does not pretend to: it captures
// the answers side by side and applies only checks that are actually decidable
// (did it use an analogy, did it define its jargon, how dense is it), leaving
// the judgement to a person reading the transcript.
//
//	abhed-modelcmp -models qwen3-coder:30b,gpt-oss:20b
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zybuu-ai/abhed/internal/model"
)

// ---------------------------------------------------------------- tool tests

type toolCase struct {
	Name     string
	Prompt   string
	Tools    []model.ToolDef
	WantCall string // "" means the model SHOULD NOT call anything
	WantArg  string // a substring that must appear in the arguments
}

func globTool() model.ToolDef {
	return model.ToolDef{
		Name:        "glob",
		Description: "Find files matching a glob pattern.",
		InputSchema: json.RawMessage(
			`{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}`),
	}
}

func readTool() model.ToolDef {
	return model.ToolDef{
		Name:        "read",
		Description: "Read the contents of a file at an absolute path.",
		InputSchema: json.RawMessage(
			`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
	}
}

func bashTool() model.ToolDef {
	return model.ToolDef{
		Name:        "bash",
		Description: "Run a shell command in the workspace.",
		InputSchema: json.RawMessage(
			`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
	}
}

func toolCases() []toolCase {
	all := []model.ToolDef{globTool(), readTool(), bashTool()}
	return []toolCase{
		{
			// The baseline: one tool, an unambiguous request.
			Name: "single tool", Prompt: "List the files matching *.go using the glob tool.",
			Tools: []model.ToolDef{globTool()}, WantCall: "glob", WantArg: ".go",
		},
		{
			// Selection: three tools offered, only one is right. A model that
			// always reaches for bash fails here, and that failure is the one
			// that makes an agent dangerous rather than merely useless.
			Name: "chooses among tools", Prompt: "Show me what is inside /etc/hosts.",
			Tools: all, WantCall: "read", WantArg: "/etc/hosts",
		},
		{
			// Restraint. Tools are offered but the question is conversational.
			// A model that calls a tool anyway will burn turns on every chat
			// message, which is exactly what makes a coding model tiring to
			// talk to.
			Name: "declines when unnecessary", Prompt: "What does the acronym API stand for?",
			Tools: all, WantCall: "",
		},
		{
			// Arguments have to survive being non-trivial.
			Name: "non-trivial arguments", Prompt: "Use bash to count the lines in go.mod.",
			Tools: all, WantCall: "bash", WantArg: "go.mod",
		},
	}
}

// ---------------------------------------------------------------- prose tests

type proseCase struct {
	Name   string
	Prompt string
}

func proseCases() []proseCase {
	return []proseCase{
		{"eli5-llm", "Explain what a large language model is, as if to a curious " +
			"12-year-old. Use an everyday analogy. Do not use bullet points or headings."},
		{"factual", "Which company created the Kubernetes project, and roughly when " +
			"was it first released? Answer in one or two sentences."},
		{"why-not-what", "Why does a neural network need an activation function at all? " +
			"Explain the reason, not the definition."},
	}
}

// ---------------------------------------------------------------- scoring

type toolResult struct {
	Case    string  `json:"case"`
	Called  string  `json:"called"`
	Args    string  `json:"args"`
	Pass    bool    `json:"pass"`
	Why     string  `json:"why"`
	Seconds float64 `json:"seconds"`
}

type proseResult struct {
	Case      string  `json:"case"`
	Answer    string  `json:"answer"`
	Words     int     `json:"words"`
	Headings  int     `json:"headings"`
	Bullets   int     `json:"bullets"`
	Analogy   bool    `json:"analogy"`
	Seconds   float64 `json:"seconds"`
	FirstByte float64 `json:"first_byte_seconds"`
}

type modelReport struct {
	Model     string        `json:"model"`
	ToolPass  int           `json:"tool_pass"`
	ToolTotal int           `json:"tool_total"`
	Tools     []toolResult  `json:"tools"`
	Prose     []proseResult `json:"prose"`
	Err       string        `json:"error,omitempty"`
}

var (
	headingRe = regexp.MustCompile(`(?m)^\s*#{1,6}\s`)
	bulletRe  = regexp.MustCompile(`(?m)^\s*([-*+]|\d+\.)\s`)
	// Analogy markers. Crude on purpose: this is a hint for the reader, not a
	// score. "like" alone is too common to mean anything, so it must be
	// introducing a comparison.
	analogyRe = regexp.MustCompile(`(?i)\b(imagine|think of it (as|like)|is like a|similar to|` +
		`picture a|kind of like|the way (a|an|you))\b`)
)

func runTools(ctx context.Context, ad model.Adapter, tc toolCase) toolResult {
	start := time.Now()
	r := toolResult{Case: tc.Name}
	stream, err := ad.Complete(ctx, model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Content: tc.Prompt}},
		Tools:     tc.Tools,
		MaxTokens: 512,
	})
	if err != nil {
		r.Why = err.Error()
		return r
	}
	var text strings.Builder
	for c := range stream {
		switch c.Type {
		case model.ChunkToolCall:
			if r.Called == "" {
				r.Called = c.ToolCall.Name
				r.Args = string(c.ToolCall.Args)
			}
		case model.ChunkText:
			text.WriteString(c.Text)
		case model.ChunkError:
			r.Why = c.Err.Error()
		}
	}
	r.Seconds = time.Since(start).Seconds()

	switch {
	case tc.WantCall == "":
		if r.Called != "" {
			r.Why = "called " + r.Called + " for a question needing no tool"
			return r
		}
		if strings.TrimSpace(text.String()) == "" {
			r.Why = "no tool call and no answer"
			return r
		}
		r.Pass = true
		r.Why = "answered directly"
	case r.Called == "":
		// The failure that matters most: describing the call instead of
		// making it. Worth naming explicitly, because the transcript looks
		// plausible and the loop still cannot dispatch anything.
		if strings.Contains(text.String(), tc.WantCall) {
			r.Why = "described the call in prose instead of emitting one"
		} else {
			r.Why = "no tool call"
		}
	case r.Called != tc.WantCall:
		r.Why = "called " + r.Called + ", expected " + tc.WantCall
	case tc.WantArg != "" && !strings.Contains(r.Args, tc.WantArg):
		r.Why = "arguments missing " + tc.WantArg
	default:
		r.Pass = true
		r.Why = "ok"
	}
	return r
}

func runProse(ctx context.Context, ad model.Adapter, pc proseCase) proseResult {
	start := time.Now()
	r := proseResult{Case: pc.Name}
	stream, err := ad.Complete(ctx, model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Content: pc.Prompt}},
		MaxTokens: 900,
	})
	if err != nil {
		r.Answer = "ERROR: " + err.Error()
		return r
	}
	var b strings.Builder
	for c := range stream {
		if c.Type == model.ChunkText {
			if b.Len() == 0 && strings.TrimSpace(c.Text) != "" {
				r.FirstByte = time.Since(start).Seconds()
			}
			b.WriteString(c.Text)
		}
	}
	r.Seconds = time.Since(start).Seconds()
	r.Answer = strings.TrimSpace(b.String())
	r.Words = len(strings.Fields(r.Answer))
	r.Headings = len(headingRe.FindAllString(r.Answer, -1))
	r.Bullets = len(bulletRe.FindAllString(r.Answer, -1))
	r.Analogy = analogyRe.MatchString(r.Answer)
	return r
}

func main() {
	var (
		baseURL   = flag.String("base-url", "http://localhost:11434/v1", "OpenAI-compatible endpoint")
		models    = flag.String("models", "", "comma-separated model tags to compare")
		jsonOut   = flag.String("json", "", "also write full results here")
		timeout   = flag.Duration("timeout", 5*time.Minute, "per-request timeout")
		toolsOnly = flag.Bool("tools-only", false, "skip the prose cases")
		ctxWindow = flag.Int("context", 65536, "context window to advertise")
		think     = flag.String("think", "", "thinking phase: on|off (default: server's own)")
	)
	flag.Parse()

	if *models == "" {
		fmt.Fprintln(os.Stderr, "usage: abhed-modelcmp -models qwen3-coder:30b,gpt-oss:20b")
		os.Exit(2)
	}

	var reports []modelReport
	for _, name := range strings.Split(*models, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		fmt.Printf("\n=== %s ===\n", name)
		ad := model.NewOpenAICompatible(*baseURL, "", name, model.Profile{
			Name:            name,
			ContextWindow:   *ctxWindow,
			MaxOutputTokens: 4096,
			SupportsTools:   true,
			SupportsStream:  true,
			ToolCallFormat:  "json",
		})
		switch *think {
		case "on":
			t := true
			ad.Think = &t
		case "off":
			t := false
			ad.Think = &t
		}
		rep := modelReport{Model: name}

		for _, tc := range toolCases() {
			ctx, cancel := context.WithTimeout(context.Background(), *timeout)
			res := runTools(ctx, ad, tc)
			cancel()
			rep.Tools = append(rep.Tools, res)
			rep.ToolTotal++
			mark := "FAIL"
			if res.Pass {
				mark = "pass"
				rep.ToolPass++
			}
			fmt.Printf("  tool  %-26s %-4s  %s  (%.1fs)\n", tc.Name, mark, res.Why, res.Seconds)
		}

		if !*toolsOnly {
			for _, pc := range proseCases() {
				ctx, cancel := context.WithTimeout(context.Background(), *timeout)
				res := runProse(ctx, ad, pc)
				cancel()
				rep.Prose = append(rep.Prose, res)
				fmt.Printf("  prose %-26s %4d words, %d headings, %d bullets, analogy=%v (%.1fs, first %.2fs)\n",
					pc.Name, res.Words, res.Headings, res.Bullets, res.Analogy,
					res.Seconds, res.FirstByte)
			}
		}
		reports = append(reports, rep)
	}

	fmt.Printf("\n%-26s %-12s %s\n", "MODEL", "TOOL CALLS", "ELI5 SHAPE")
	sort.Slice(reports, func(i, j int) bool { return reports[i].ToolPass > reports[j].ToolPass })
	for _, r := range reports {
		shape := "—"
		for _, p := range r.Prose {
			if p.Case == "eli5-llm" {
				shape = fmt.Sprintf("%d words, %d headings, analogy=%v",
					p.Words, p.Headings, p.Analogy)
			}
		}
		fmt.Printf("%-26s %d/%-10d %s\n", r.Model, r.ToolPass, r.ToolTotal, shape)
	}

	if *jsonOut != "" {
		f, err := os.Create(*jsonOut)
		if err != nil {
			fmt.Fprintf(os.Stderr, "abhed-modelcmp: %v\n", err)
			os.Exit(1)
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		err = enc.Encode(reports)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "abhed-modelcmp: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\nfull transcripts: %s\n", *jsonOut)
	}
}
