// Command titan is an on-prem coding agent.
//
// Usage:
//
//	titan                      interactive session in the current directory
//	titan -p "fix the tests"   headless; exit code reflects the terminal event
//	titan init                 write a starter config
//	titan doctor               check that the configured endpoint works
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yuvrajsingh/titan/internal/agent"
	"github.com/yuvrajsingh/titan/internal/config"
	"github.com/yuvrajsingh/titan/internal/index"
	"github.com/yuvrajsingh/titan/internal/mcp"
	"github.com/yuvrajsingh/titan/internal/model"
	"github.com/yuvrajsingh/titan/internal/policy"
	"github.com/yuvrajsingh/titan/internal/sandbox"
	"github.com/yuvrajsingh/titan/internal/server"
	"github.com/yuvrajsingh/titan/internal/store"
	"github.com/yuvrajsingh/titan/internal/tools"
	"github.com/yuvrajsingh/titan/internal/ui"
)

var version = "0.1.0-dev"

func main() {
	var (
		prompt     = flag.String("p", "", "run headless with this prompt and exit")
		mode       = flag.String("mode", "", "permission mode: default|accept-edits|plan|auto|bypass")
		modelID    = flag.String("model", "", "provider name from config")
		workdir    = flag.String("C", "", "workspace directory (default: current)")
		maxTurns   = flag.Int("max-turns", 0, "override the turn limit")
		format     = flag.String("output-format", "text", "text|json")
		allow      = flag.String("allow", "", "comma-separated allow rules, e.g. 'bash(go test*)'")
		deny       = flag.String("deny", "", "comma-separated deny rules")
		showVer    = flag.Bool("version", false, "print version and exit")
		listenAddr = flag.String("addr", ":8080", "listen address for `titan serve`")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("titan", version)
		return
	}

	workspace, err := resolveWorkspace(*workdir)
	if err != nil {
		fail(err)
	}

	switch flag.Arg(0) {
	case "init":
		path := filepath.Join(workspace, ".titan", "config.json")
		if err := config.WriteDefault(path); err != nil {
			fail(err)
		}
		fmt.Printf("Wrote %s\nEdit it to point at your model endpoint, then run `titan doctor`.\n", path)
		return
	case "doctor":
		os.Exit(doctor(workspace))
	case "index":
		os.Exit(buildIndexCmd(workspace))
	case "serve":
		// Re-parse the remaining args so `titan serve -addr :9000` works: Go's
		// flag package stops at the first non-flag argument.
		serveFlags := flag.NewFlagSet("serve", flag.ExitOnError)
		serveAddr := serveFlags.String("addr", *listenAddr, "listen address")
		_ = serveFlags.Parse(flag.Args()[1:])
		os.Exit(serveCmd(workspace, *serveAddr))
	}

	os.Exit(run(workspace, *prompt, *mode, *modelID, *maxTurns, *format, *allow, *deny))
}

func run(workspace, prompt, modeFlag, modelFlag string, maxTurns int, format, allowFlag, denyFlag string) int {
	cfg, err := config.Load(workspace)
	if err != nil {
		fail(err)
	}
	if modelFlag != "" {
		cfg.Model.Default = modelFlag
	}
	if modeFlag != "" {
		cfg.Permissions.Mode = modeFlag
	}
	if maxTurns > 0 {
		cfg.Limits.MaxTurns = maxTurns
	}

	provider, err := cfg.Provider()
	if err != nil {
		fail(err)
	}

	adapter := buildAdapter(provider)
	sess, err := tools.NewSession(workspace)
	if err != nil {
		fail(err)
	}

	pol := policy.New(policy.Mode(orDefault(cfg.Permissions.Mode, "default")))
	pol.Managed = cfg.Managed
	must(pol.AddDeny(cfg.Permissions.Deny...))
	must(pol.AddAsk(cfg.Permissions.Ask...))
	must(pol.AddAllow(cfg.Permissions.Allow...))
	must(pol.AddAllow(splitRules(allowFlag)...))
	must(pol.AddDeny(splitRules(denyFlag)...))

	sb, err := buildSandbox(cfg, workspace)
	if err != nil {
		fail(err)
	}
	if sb.Tier() == sandbox.TierNone {
		fmt.Fprintf(os.Stderr, "titan: warning: %s\n", sb.Describe())
	}

	registry := tools.NewRegistry(
		tools.Read{}, tools.Write{}, tools.Edit{},
		tools.Glob{}, tools.Grep{}, tools.Bash{Sandbox: sb.Command},
	)

	// Subagents share the parent's budget, so a fan-out cannot multiply spend
	// invisibly. Each spawn re-prefills its own prefix (docs P3).
	budget := agent.NewBudget(
		int64(cfg.Limits.MaxBudgetTokens),
		cfg.Limits.MaxSubagents,
		cfg.Limits.NestedSubagents,
	)

	// MCP servers extend the tool surface. Every remote tool is namespaced and
	// routes through the policy engine, since Titan cannot know what it does.
	gateway := mcp.NewGateway()
	defer gateway.Close()
	if mcpErrs := gateway.Connect(context.Background(), mcpConfigs(cfg)); len(mcpErrs) > 0 {
		for _, e := range mcpErrs {
			fmt.Fprintf(os.Stderr, "titan: %v\n", e)
		}
	}
	for _, t := range gateway.Tools() {
		registry.Add(t)
	}

	// Retrieval is tier 2: an accelerator over grep, not a replacement.
	if cfg.Retrieval.Enabled {
		if ix, err := openIndex(context.Background(), cfg, workspace); err != nil {
			fmt.Fprintf(os.Stderr, "titan: index unavailable, falling back to grep: %v\n", err)
		} else {
			registry.Add(&index.SearchTool{Index: ix})
		}
	}

	systemPrompt := agent.BuildSystemPrompt(agent.BuildOptions{
		Profile:       "main",
		Workspace:     workspace,
		Model:         provider.Model,
		ContextWindow: provider.ContextWindow,
		MemoryFiles:   agent.DiscoverMemoryFiles(workspace),
	})

	loopCfg := agent.DefaultConfig()
	loopCfg.SystemPrompt = systemPrompt
	loopCfg.MaxTurns = cfg.Limits.MaxTurns
	loopCfg.MaxTokens = cfg.Limits.MaxTokens
	loopCfg.CompactAt = cfg.Context.CompactAt

	factory := &agent.SubagentFactory{
		Adapter: adapter, Tools: registry, Policy: pol,
		Approver: agent.AutoApprove{Yes: true}, // subagent tools are policed by pol
		Session:  sess, Budget: budget, Config: loopCfg, Workspace: workspace,
	}
	registry.Add(agent.Task{Spawn: factory.Spawn, Profiles: agent.Profiles})

	headless := prompt != ""
	jsonOut := format == "json"

	store := agent.NewMemStore()
	factory.Store = store
	renderer := ui.NewRenderer(os.Stdout, jsonOut)

	var approver agent.Approver
	if headless {
		// No TTY to ask: policy alone decides, and anything needing approval
		// is refused rather than silently allowed.
		approver = agent.AutoApprove{Yes: false}
	} else {
		approver = ui.NewApprover(os.Stdout)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if headless {
		return runOnce(ctx, store, renderer, jsonOut, adapter, registry, pol, approver, sess, loopCfg, prompt)
	}
	return interactive(ctx, store, renderer, adapter, registry, pol, approver, sess, loopCfg, provider, workspace)
}

func runOnce(ctx context.Context, store *agent.MemStore, r *ui.Renderer, jsonOut bool,
	adapter model.Adapter, registry *tools.Registry, pol *policy.Engine,
	approver agent.Approver, sess *tools.Session, cfg agent.Config, prompt string) int {

	sessionID := fmt.Sprintf("s-%d", time.Now().UnixNano())
	rec := agent.NewRecorder(store, sessionID, "")

	events := store.Subscribe(sessionID)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range events {
			if jsonOut {
				b, _ := json.Marshal(ev)
				fmt.Println(string(b))
			} else {
				r.Event(ev)
			}
		}
	}()

	loop := agent.NewLoop(adapter, registry, pol, approver, sess, rec, cfg)
	loop.Compactor = agent.NewCompactor(adapter, cfg.CompactAt)
	reason, err := loop.Run(ctx, prompt)

	store.Unsubscribe(sessionID, events)
	<-done

	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		return agent.TermError.ExitCode()
	}
	if !jsonOut {
		printUsage(r, loop.Usage())
	}
	return reason.ExitCode()
}

func interactive(ctx context.Context, store *agent.MemStore, r *ui.Renderer,
	adapter model.Adapter, registry *tools.Registry, pol *policy.Engine,
	approver agent.Approver, sess *tools.Session, cfg agent.Config,
	provider config.ProviderConfig, workspace string) int {

	s := r.Style()
	fmt.Printf("%s %s  %s\n", s.Bold("titan"), s.Dim(version),
		s.Dim(fmt.Sprintf("%s · %s", provider.Model, workspace)))
	fmt.Printf("%s\n\n", s.Dim("Type a task, or /help for commands. Ctrl-C interrupts, Ctrl-D exits."))

	in := bufio.NewReader(os.Stdin)
	turn := 0

	for {
		fmt.Printf("%s ", s.Cyan("›"))
		line, err := in.ReadString('\n')
		if err != nil {
			fmt.Println()
			return 0
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			if quit := handleCommand(line, r, pol, sess); quit {
				return 0
			}
			continue
		}

		turn++
		sessionID := fmt.Sprintf("s-%d-%d", time.Now().Unix(), turn)
		rec := agent.NewRecorder(store, sessionID, "")

		events := store.Subscribe(sessionID)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for ev := range events {
				r.Event(ev)
			}
		}()

		// Each task gets its own cancellable context so Ctrl-C interrupts the
		// task without killing the session.
		taskCtx, cancelTask := context.WithCancel(ctx)
		loop := agent.NewLoop(adapter, registry, pol, approver, sess, rec, cfg)
		loop.Compactor = agent.NewCompactor(adapter, cfg.CompactAt)
		_, runErr := loop.Run(taskCtx, line)
		cancelTask()

		store.Unsubscribe(sessionID, events)
		<-done

		if runErr != nil {
			fmt.Printf("%s %s\n", s.Red("error:"), runErr)
		}
		printUsage(r, loop.Usage())
		fmt.Println()

		if ctx.Err() != nil {
			return 130
		}
	}
}

func handleCommand(line string, r *ui.Renderer, pol *policy.Engine, sess *tools.Session) bool {
	s := r.Style()
	fields := strings.Fields(line)
	switch fields[0] {
	case "/quit", "/exit":
		return true
	case "/help":
		fmt.Println(s.Dim(`  /mode <name>   default | accept-edits | plan | auto
  /cwd           show the workspace root
  /quit          exit`))
	case "/mode":
		if len(fields) < 2 {
			fmt.Printf("  current mode: %s\n", pol.Mode)
			return false
		}
		m := policy.Mode(fields[1])
		switch m {
		case policy.ModeDefault, policy.ModeAcceptEdits, policy.ModePlan, policy.ModeAuto:
			pol.Mode = m
			fmt.Printf("  mode: %s\n", m)
		default:
			fmt.Printf("  %s unknown mode %q\n", s.Red("✕"), fields[1])
		}
	case "/cwd":
		fmt.Printf("  %s\n", sess.Root)
	default:
		fmt.Printf("  %s unknown command %s\n", s.Red("✕"), fields[0])
	}
	return false
}

func printUsage(r *ui.Renderer, u agent.Usage) {
	s := r.Style()
	line := fmt.Sprintf("%d turns · %d in / %d out tokens", u.Turns, u.InputTokens, u.OutputTokens)
	// Cache hit rate is a UX metric as much as a capacity one (docs P8), so it
	// is shown rather than hidden in telemetry.
	if u.InputTokens > 0 && u.CachedTokens > 0 {
		line += fmt.Sprintf(" · %.0f%% cached (%.1fx prefill)", u.CacheHitRate()*100, u.PrefillSavings())
	}
	if u.Compactions > 0 {
		line += fmt.Sprintf(" · %d compaction(s)", u.Compactions)
	}
	fmt.Printf("%s\n", s.Dim(line))
}

// serveCmd starts server mode: web console, REST API, and SSE streaming over
// the same event stream the CLI consumes.
func serveCmd(workspace, addr string) int {
	cfg, err := config.Load(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "titan: %v\n", err)
		return 1
	}
	provider, err := cfg.Provider()
	if err != nil {
		fmt.Fprintf(os.Stderr, "titan: %v\n", err)
		return 1
	}

	sb, err := buildSandbox(cfg, workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "titan: %v\n", err)
		return 1
	}
	registry := tools.NewRegistry(
		tools.Read{}, tools.Write{}, tools.Edit{},
		tools.Glob{}, tools.Grep{}, tools.Bash{Sandbox: sb.Command},
	)

	gateway := mcp.NewGateway()
	defer gateway.Close()
	gateway.Connect(context.Background(), mcpConfigs(cfg))
	for _, t := range gateway.Tools() {
		registry.Add(t)
	}
	fmt.Printf("storage     %s\n", storageLabel(cfg))
	if cfg.Storage.Driver == "postgres" {
		st, closeFn, err := openStore(context.Background(), cfg)
		if err != nil {
			fmt.Printf("            UNAVAILABLE — %v\n", err)
		} else {
			if pg, ok := st.(*store.Postgres); ok {
				if sessions, events, err := pg.Stats(context.Background()); err == nil {
					fmt.Printf("            %d sessions · %d events persisted\n", sessions, events)
				}
			}
			closeFn()
		}
	}
	if cfg.Retrieval.Enabled {
		if ix, err := openIndex(context.Background(), cfg, workspace); err == nil {
			registry.Add(&index.SearchTool{Index: ix})
		}
	}

	eventStore, closeStore, err := openStore(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "titan: %v\n", err)
		return 1
	}
	defer closeStore()

	srv := server.New(server.Options{
		Addr:      addr,
		Workspace: workspace,
		Config:    cfg,
		Adapter:   buildAdapter(provider),
		Registry:  registry,
		Store:     eventStore,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("titan %s serving on http://localhost%s\n", version, addr)
	fmt.Printf("  workspace %s\n  model     %s\n  sandbox   %s\n  storage   %s\n",
		workspace, provider.Model, sb.Tier(), storageLabel(cfg))

	if err := srv.ListenAndServe(ctx); err != nil && err.Error() != "http: Server closed" {
		fmt.Fprintf(os.Stderr, "titan: %v\n", err)
		return 1
	}
	return 0
}

// openStore selects the event store. Memory is fine for a CLI session; audit
// and replay across restarts need Postgres.
func openStore(ctx context.Context, cfg config.Config) (server.EventStore, func(), error) {
	if cfg.Storage.Driver != "postgres" {
		return agent.NewMemStore(), func() {}, nil
	}
	sc := store.DefaultConfig(cfg.Storage.DSN)
	if cfg.Storage.Tenant != "" {
		sc.Tenant = cfg.Storage.Tenant
	}
	if cfg.Storage.MaxConns > 0 {
		sc.MaxConns = int32(cfg.Storage.MaxConns)
	}
	pg, err := store.Open(ctx, sc)
	if err != nil {
		return nil, nil, fmt.Errorf("open event store: %w", err)
	}
	return pg, pg.Close, nil
}

func storageLabel(cfg config.Config) string {
	if cfg.Storage.Driver == "postgres" {
		return "postgres (durable, tenant=" + orDefault(cfg.Storage.Tenant, "default") + ")"
	}
	return "memory (sessions do not survive restart)"
}

// openIndex builds the retrieval index for this workspace.
func openIndex(ctx context.Context, cfg config.Config, workspace string) (*index.Index, error) {
	ix := index.New(workspace)
	opts := index.DefaultBuildOptions()

	if cfg.Retrieval.Embed && cfg.Retrieval.EmbedBaseURL != "" {
		provider, _ := cfg.Provider()
		ix = ix.WithEmbedder(index.NewOpenAIEmbedder(
			cfg.Retrieval.EmbedBaseURL, provider.APIKey,
			cfg.Retrieval.EmbedModel, cfg.Retrieval.EmbedDims))
		opts.Embed = true
	}
	if err := ix.Build(ctx, opts); err != nil {
		return nil, err
	}
	return ix, nil
}

// buildIndexCmd implements `titan index`, so a large repo can be indexed once
// rather than on every session start.
func buildIndexCmd(workspace string) int {
	cfg, err := config.Load(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "titan: %v\n", err)
		return 1
	}
	fmt.Printf("indexing %s...\n", workspace)
	start := time.Now()

	ix, err := openIndex(context.Background(), cfg, workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "titan: %v\n", err)
		return 1
	}
	docs, terms, vectors, _ := ix.Stats()
	fmt.Printf("  %d chunks · %d terms · %d vectors · %s\n",
		docs, terms, vectors, time.Since(start).Round(time.Millisecond))
	if vectors == 0 {
		fmt.Printf("  (symbol + BM25 tiers only; set retrieval.embed to add the vector tier)\n")
	}
	return 0
}

func mcpConfigs(cfg config.Config) []mcp.ServerConfig {
	out := make([]mcp.ServerConfig, 0, len(cfg.MCP.Servers))
	for _, s := range cfg.MCP.Servers {
		out = append(out, mcp.ServerConfig{
			Name: s.Name, Command: s.Command, Args: s.Args, Env: s.Env,
			Enabled: s.Enabled, AllowTools: s.AllowTools, Digest: s.Digest,
		})
	}
	return out
}

// buildSandbox selects an execution backend meeting the configured minimum
// tier. Select never silently downgrades, so a failure here is a real
// configuration problem the operator must see.
func buildSandbox(cfg config.Config, workspace string) (sandbox.Sandbox, error) {
	p := sandbox.DefaultPolicy(workspace)
	if cfg.Sandbox.MinTier != "" {
		p.MinTier = sandbox.Tier(cfg.Sandbox.MinTier)
	}
	p.AllowNetwork = cfg.Sandbox.AllowNetwork
	p.ReadOnlyPaths = cfg.Sandbox.ReadOnlyPaths
	if cfg.Sandbox.MaxMemoryMB > 0 {
		p.MaxMemoryMB = cfg.Sandbox.MaxMemoryMB
	}
	if cfg.Sandbox.MaxProcs > 0 {
		p.MaxProcs = cfg.Sandbox.MaxProcs
	}
	return sandbox.Select(p)
}

func buildAdapter(p config.ProviderConfig) model.Adapter {
	profile := model.Profile{
		Name:            p.Model,
		ContextWindow:   p.ContextWindow,
		MaxOutputTokens: p.MaxOutputTokens,
		SupportsTools:   true,
		SupportsStream:  true,
		ToolCallFormat:  orDefault(p.ToolCallFormat, "json"),
	}
	a := model.NewOpenAICompatible(p.BaseURL, p.APIKey, p.Model, profile)
	if len(p.ReasoningTags) == 2 {
		a.ReasoningTags = [2]string{p.ReasoningTags[0], p.ReasoningTags[1]}
	}
	return a
}

// doctor verifies the endpoint actually works before the user debugs it
// through a failing agent run.
func doctor(workspace string) int {
	cfg, err := config.Load(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	provider, err := cfg.Provider()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}

	fmt.Printf("workspace   %s\n", workspace)
	fmt.Printf("provider    %s (%s)\n", cfg.Model.Default, provider.Type)
	fmt.Printf("endpoint    %s\n", provider.BaseURL)
	fmt.Printf("model       %s\n", provider.Model)
	fmt.Printf("mode        %s\n", orDefault(cfg.Permissions.Mode, "default"))
	if sb, err := buildSandbox(cfg, workspace); err == nil {
		label := string(sb.Tier())
		if sb.Tier() == sandbox.TierNone {
			label += "  ⚠"
		}
		fmt.Printf("sandbox     %s — %s\n", label, sb.Describe())
	} else {
		fmt.Printf("sandbox     UNAVAILABLE — %v\n", err)
	}
	if files := agent.DiscoverMemoryFiles(workspace); len(files) > 0 {
		fmt.Printf("memory      %s\n", strings.Join(files, ", "))
	}
	fmt.Printf("storage     %s\n", storageLabel(cfg))
	if cfg.Storage.Driver == "postgres" {
		st, closeFn, err := openStore(context.Background(), cfg)
		if err != nil {
			fmt.Printf("            UNAVAILABLE — %v\n", err)
		} else {
			if pg, ok := st.(*store.Postgres); ok {
				if sessions, events, err := pg.Stats(context.Background()); err == nil {
					fmt.Printf("            %d sessions · %d events persisted\n", sessions, events)
				}
			}
			closeFn()
		}
	}
	if cfg.Retrieval.Enabled {
		if ix, err := openIndex(context.Background(), cfg, workspace); err == nil {
			d, t, v, _ := ix.Stats()
			fmt.Printf("index       %d chunks · %d terms · %d vectors\n", d, t, v)
		} else {
			fmt.Printf("index       UNAVAILABLE — %v\n", err)
		}
	}
	if servers := cfg.MCP.Servers; len(servers) > 0 {
		gw := mcp.NewGateway()
		gw.Connect(context.Background(), mcpConfigs(cfg))
		status := gw.Status()
		gw.Close()
		if len(status) > 0 {
			fmt.Printf("mcp         %s\n", strings.Join(status, ", "))
		} else {
			fmt.Printf("mcp         %d configured, none connected\n", len(servers))
		}
	}
	fmt.Println()

	adapter := buildAdapter(provider)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Print("checking endpoint... ")
	stream, err := adapter.Complete(ctx, model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Content: "Reply with the single word: ok"}},
		MaxTokens: 32,
	})
	if err != nil {
		fmt.Printf("FAILED\n  %v\n", err)
		fmt.Println("\nCheck that the endpoint is reachable and the model name is correct.")
		return 1
	}
	var got strings.Builder
	for c := range stream {
		if c.Type == model.ChunkText {
			got.WriteString(c.Text)
		}
		if c.Type == model.ChunkError {
			fmt.Printf("FAILED\n  %v\n", c.Err)
			return 1
		}
	}
	fmt.Printf("ok\n  response: %q\n", strings.TrimSpace(got.String()))

	// Tool calling is the capability the agent actually depends on.
	fmt.Print("checking tool calling... ")
	stream, err = adapter.Complete(ctx, model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "List files matching *.go using the glob tool."}},
		Tools: []model.ToolDef{{
			Name:        "glob",
			Description: "Find files matching a glob pattern.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}`),
		}},
		MaxTokens: 256,
	})
	if err != nil {
		fmt.Printf("FAILED\n  %v\n", err)
		return 1
	}
	calls := 0
	for c := range stream {
		if c.Type == model.ChunkToolCall {
			calls++
			fmt.Printf("ok\n  called %s with %s\n", c.ToolCall.Name, c.ToolCall.Args)
		}
	}
	if calls == 0 {
		fmt.Println("FAILED")
		fmt.Println("  The model did not emit a tool call. Titan requires tool-calling support.")
		fmt.Println("  Check that the serving stack has a tool-call parser enabled for this model.")
		return 1
	}

	fmt.Println("\nReady.")
	return 0
}

func resolveWorkspace(dir string) (string, error) {
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}

func splitRules(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func must(err error) {
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "titan: %v\n", err)
	os.Exit(1)
}
