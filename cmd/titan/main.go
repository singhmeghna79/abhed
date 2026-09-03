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
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/yuvrajsingh/titan/internal/agent"
	"github.com/yuvrajsingh/titan/internal/auth"
	"github.com/yuvrajsingh/titan/internal/config"
	"github.com/yuvrajsingh/titan/internal/eval"
	"github.com/yuvrajsingh/titan/internal/index"
	"github.com/yuvrajsingh/titan/internal/mcp"
	"github.com/yuvrajsingh/titan/internal/model"
	"github.com/yuvrajsingh/titan/internal/policy"
	"github.com/yuvrajsingh/titan/internal/sandbox"
	"github.com/yuvrajsingh/titan/internal/server"
	"github.com/yuvrajsingh/titan/internal/store"
	"github.com/yuvrajsingh/titan/internal/tools"
	"github.com/yuvrajsingh/titan/internal/ui"
	"github.com/yuvrajsingh/titan/internal/websearch"
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
	case "eval":
		evalFlags := flag.NewFlagSet("eval", flag.ExitOnError)
		corpus := evalFlags.String("corpus", "internal/eval/corpus", "task corpus directory")
		jsonOut := evalFlags.String("json", "", "write the full report to this path")
		_ = evalFlags.Parse(flag.Args()[1:])
		os.Exit(evalCmd(workspace, *corpus, *jsonOut))
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

	if t, err := buildWebSearch(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "titan: web search disabled: %v\n", err)
	} else if t != nil {
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

	// The CLI uses whatever the config selects. Previously this was hardcoded
	// to memory, so a Postgres-configured deployment silently lost its CLI
	// sessions while server sessions persisted — an inconsistency the user
	// would only discover when an audit came up empty.
	store, closeStore, err := openStore(context.Background(), cfg)
	if err != nil {
		fail(err)
	}
	defer closeStore()
	factory.Store = store
	renderer := ui.NewRenderer(os.Stdout, jsonOut)

	var approver agent.Approver
	if headless {
		// No TTY to ask. In auto mode the operator has already delegated the
		// decision to the policy engine, so anything reaching the approver is
		// something policy chose not to allow outright — approving it here
		// would defeat the mode's own rules. In every other mode a headless run
		// cannot obtain consent, so it refuses.
		//
		// Either way the model must be told WHY, or it retries blindly: the
		// first real run against a local model spent 20 turns re-phrasing the
		// same rejected command.
		approver = agent.AutoApprove{Yes: false}
	} else {
		approver = ui.NewApprover(os.Stdout)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if headless {
		return runOnce(ctx, store, renderer, jsonOut, adapter, registry, pol, approver, sess, loopCfg, cfg, prompt)
	}
	return interactive(ctx, store, renderer, adapter, registry, pol, approver, sess, loopCfg, cfg, provider, workspace)
}

func runOnce(ctx context.Context, store server.EventStore, r *ui.Renderer, jsonOut bool,
	adapter model.Adapter, registry *tools.Registry, pol *policy.Engine,
	approver agent.Approver, sess *tools.Session, cfg agent.Config,
	appCfg config.Config, prompt string) int {

	sessionID := fmt.Sprintf("s-%d", time.Now().UnixNano())
	recordSession(ctx, store, sessionID, appCfg, prompt)
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

func interactive(ctx context.Context, store server.EventStore, r *ui.Renderer,
	adapter model.Adapter, registry *tools.Registry, pol *policy.Engine,
	approver agent.Approver, sess *tools.Session, cfg agent.Config,
	appCfg config.Config, provider config.ProviderConfig, workspace string) int {

	s := r.Style()
	sandboxLabel := "none"
	if sb, err := buildSandbox(appCfg, workspace); err == nil {
		sandboxLabel = string(sb.Tier())
		if !appCfg.Sandbox.AllowNetwork {
			sandboxLabel += " · no network"
		}
	}
	fmt.Print(ui.Banner(s, version, provider.Model, workspace,
		sandboxLabel, storageLabel(appCfg)))
	fmt.Printf("\n%s\n\n", s.Dim("Type a task, or /help. Ctrl-C interrupts, Ctrl-D exits."))

	in := bufio.NewReader(os.Stdin)
	turn := 0
	// Session-level state the slash commands operate on.
	undo := agent.NewUndoLog()
	sess.Checkpoint = undo.Record
	sessionState := &cliState{
		store: store, appCfg: appCfg, undo: undo,
		workspace: sess.Root, adapter: adapter, provider: provider,
	}

	for {
		fmt.Print(ui.Prompt(s))
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
			if quit := handleCommand(ctx, line, r, pol, sess, sessionState); quit {
				return 0
			}
			continue
		}

		turn++
		sessionID := fmt.Sprintf("s-%d-%d", time.Now().Unix(), turn)
		recordSession(ctx, store, sessionID, appCfg, line)
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
		sessionState.loop = loop
		sessionState.sessionID = sessionID
		undo.BeginTurn()
		_, runErr := loop.Run(taskCtx, line)
		cancelTask()
		sessionState.accumulate(loop.Usage())

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

// cliState carries what the slash commands need across turns.
type cliState struct {
	store     server.EventStore
	appCfg    config.Config
	loop      *agent.Loop
	sessionID string
	total     agent.Usage
	undo      *agent.UndoLog
	workspace string
	adapter   model.Adapter
	provider  config.ProviderConfig
	// transcript accumulates the session for /export.
	transcript []agent.Event
}

func (c *cliState) accumulate(u agent.Usage) {
	c.total.InputTokens += u.InputTokens
	c.total.OutputTokens += u.OutputTokens
	c.total.CachedTokens += u.CachedTokens
	c.total.ColdPrefillTokens += u.ColdPrefillTokens
	c.total.Turns += u.Turns
	c.total.Compactions += u.Compactions
}

func handleCommand(ctx context.Context, line string, r *ui.Renderer,
	pol *policy.Engine, sess *tools.Session, st *cliState) bool {
	s := r.Style()
	fields := strings.Fields(line)

	switch fields[0] {
	case "/quit", "/exit":
		return true

	case "/help":
		fmt.Println(s.Dim(`  /mode <name>      default | accept-edits | plan | auto
  /undo             revert the last turn's file changes
  /diff             files changed this session
  /cost             tokens, cache hit rate, compactions this session
  /compact [hint]   compact the context now
  /clear            clear the context, keep the workspace
  /memory           show the TITAN.md files in effect
  /model [name]     show or switch the configured provider
  /sessions         list recent sessions (durable store)
  /resume <id>      replay a past session's transcript
  /export [path]    write the transcript to a JSON file
  /cwd              show the workspace root
  /quit             exit`))

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

	case "/cost":
		u := st.total
		if u.InputTokens == 0 {
			fmt.Println(s.Dim("  no usage yet this session"))
			return false
		}
		fmt.Printf("  turns          %d\n", u.Turns)
		fmt.Printf("  tokens in      %d\n", u.InputTokens)
		fmt.Printf("  tokens out     %d\n", u.OutputTokens)
		fmt.Printf("  cached         %d (%.0f%%)\n", u.CachedTokens, u.CacheHitRate()*100)
		if savings := u.PrefillSavings(); savings > 0 {
			fmt.Printf("  prefill saving %.1fx\n", savings)
		}
		fmt.Printf("  compactions    %d\n", u.Compactions)

	case "/compact":
		if st.loop == nil {
			fmt.Println(s.Dim("  nothing to compact yet"))
			return false
		}
		info, err := st.loop.Compact(ctx)
		if err != nil {
			fmt.Printf("  %s %v\n", s.Red("✕"), err)
			return false
		}
		fmt.Printf("  compacted %d → %d tokens\n", info.BeforeTokens, info.AfterTokens)

	case "/sessions":
		lister, ok := st.store.(interface {
			ListSessions(context.Context, int) ([]store.SessionRecord, error)
		})
		if !ok {
			fmt.Println(s.Dim("  session history needs storage.driver = postgres"))
			return false
		}
		records, err := lister.ListSessions(ctx, 20)
		if err != nil {
			fmt.Printf("  %s %v\n", s.Red("✕"), err)
			return false
		}
		if len(records) == 0 {
			fmt.Println(s.Dim("  no sessions recorded"))
			return false
		}
		for _, rec := range records {
			state := "running"
			if rec.EndedAt != nil {
				state = rec.TerminalReason
			}
			fmt.Printf("  %-22s %-10s %s  %s\n", rec.ID, state,
				rec.StartedAt.Format("2006-01-02 15:04"), s.Dim(rec.User))
		}

	case "/resume":
		if len(fields) < 2 {
			fmt.Println(s.Dim("  usage: /resume <session-id>   (see /sessions)"))
			return false
		}
		events, err := st.store.Events(fields[1])
		if err != nil {
			fmt.Printf("  %s %v\n", s.Red("✕"), err)
			return false
		}
		if len(events) == 0 {
			fmt.Printf("  %s no events for session %s\n", s.Red("✕"), fields[1])
			return false
		}
		fmt.Printf("%s\n", s.Dim(fmt.Sprintf("  replaying %d events from %s", len(events), fields[1])))
		for _, ev := range events {
			r.Event(ev)
		}

	case "/undo":
		restored, err := st.undo.Undo()
		for _, line := range restored {
			fmt.Printf("  %s\n", line)
		}
		if err != nil {
			fmt.Printf("  %s %v\n", s.Red("✕"), err)
			return false
		}
		fmt.Printf("  %s\n", s.Dim(fmt.Sprintf("%d turn(s) still undoable", st.undo.Pending())))

	case "/diff":
		changed := st.undo.Changed()
		if len(changed) == 0 {
			fmt.Println(s.Dim("  no files changed this session"))
			return false
		}
		for _, path := range changed {
			rel := path
			if r, err := filepath.Rel(sess.Root, path); err == nil && !strings.HasPrefix(r, "..") {
				rel = r
			}
			before, existed, _ := st.undo.Original(path)
			after, readErr := os.ReadFile(path)
			switch {
			case !existed:
				fmt.Printf("  %s %s\n", s.Green("+"), rel)
			case readErr != nil:
				fmt.Printf("  %s %s (deleted)\n", s.Red("-"), rel)
			default:
				added, removed := lineDelta(string(before), string(after))
				fmt.Printf("  %s %s  %s %s\n", s.Yellow("~"), rel,
					s.Green(fmt.Sprintf("+%d", added)), s.Red(fmt.Sprintf("-%d", removed)))
			}
		}

	case "/clear":
		st.loop = nil
		st.total = agent.Usage{}
		st.transcript = nil
		fmt.Println(s.Dim("  context cleared; the workspace is untouched"))

	case "/memory":
		files := agent.DiscoverMemoryFiles(sess.Root)
		if len(files) == 0 {
			path := filepath.Join(sess.Root, "TITAN.md")
			fmt.Printf("  %s\n", s.Dim("no memory file yet; create "+path))
			fmt.Printf("  %s\n", s.Dim("it is re-injected on every request, so keep it short"))
			return false
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			fmt.Printf("  %s %s\n", s.Bold(f), s.Dim(fmt.Sprintf("(%d bytes)", len(data))))
			for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
				fmt.Printf("    %s\n", line)
			}
		}

	case "/model":
		if len(fields) < 2 {
			fmt.Printf("  current: %s\n", st.provider.Model)
			names := make([]string, 0, len(st.appCfg.Model.Providers))
			for name := range st.appCfg.Model.Providers {
				names = append(names, name)
			}
			sort.Strings(names)
			fmt.Printf("  configured providers: %s\n", strings.Join(names, ", "))
			return false
		}
		p, found := st.appCfg.Model.Providers[fields[1]]
		if !found {
			fmt.Printf("  %s no provider %q in config\n", s.Red("✕"), fields[1])
			return false
		}
		st.appCfg.Model.Default = fields[1]
		st.provider = p
		fmt.Printf("  %s\n", s.Dim("switched to "+p.Model+"; restart titan for it to take effect"))
		fmt.Printf("  %s\n", s.Dim("(mid-session switching would invalidate the prefix cache)"))

	case "/export":
		path := filepath.Join(sess.Root, fmt.Sprintf("titan-session-%s.json", st.sessionID))
		if len(fields) > 1 {
			path = fields[1]
		}
		events, err := st.store.Events(st.sessionID)
		if err != nil || len(events) == 0 {
			fmt.Println(s.Dim("  no transcript to export yet"))
			return false
		}
		data, err := json.MarshalIndent(events, "", "  ")
		if err != nil {
			fmt.Printf("  %s %v\n", s.Red("✕"), err)
			return false
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			fmt.Printf("  %s %v\n", s.Red("✕"), err)
			return false
		}
		fmt.Printf("  wrote %d events to %s\n", len(events), path)

	case "/cwd":
		fmt.Printf("  %s\n", sess.Root)

	default:
		fmt.Printf("  %s unknown command %s — try /help\n", s.Red("✕"), fields[0])
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
	fmt.Printf("auth        %s\n", authLabel(cfg))
	if cfg.Auth.Mode == "oidc" {
		if _, err := buildAuth(cfg); err != nil {
			fmt.Printf("            UNAVAILABLE — %v\n", err)
		} else {
			fmt.Printf("            JWKS reachable, tokens will be verified\n")
		}
	}
	fmt.Printf("web search  %s\n", webSearchLabel(cfg))
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
	if t, err := buildWebSearch(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "titan: web search disabled: %v\n", err)
	} else if t != nil {
		registry.Add(t)
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

	authMW, err := buildAuth(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "titan: %v\n", err)
		return 1
	}

	srv := server.New(server.Options{
		Addr:      addr,
		Workspace: workspace,
		Config:    cfg,
		Adapter:   buildAdapter(provider),
		Registry:  registry,
		Store:     eventStore,
		Auth:      authMW,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bs := ui.NewStyle(os.Stdout)
	fmt.Printf("%s %s %s  http://localhost%s\n", bs.Cyan(ui.Glyph),
		bs.Bold("TITAN"), bs.Dim(version), addr)
	fmt.Printf("  workspace %s\n  model     %s\n  sandbox   %s\n  storage   %s\n",
		workspace, provider.Model, sb.Tier(), storageLabel(cfg))
	fmt.Printf("  auth      %s\n", authLabel(cfg))

	if err := srv.ListenAndServe(ctx); err != nil && err.Error() != "http: Server closed" {
		fmt.Fprintf(os.Stderr, "titan: %v\n", err)
		return 1
	}
	return 0
}

// evalCmd runs the evaluation corpus against the configured model.
//
// Per docs P1 the harness is the dominant variable in agent success, so this is
// how a harness change is judged. Per P10 the report carries behavioural flags
// alongside the score, because identical pass rates hide different behaviour.
func evalCmd(workspace, corpusDir, jsonPath string) int {
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

	tasks, err := eval.LoadTasks(corpusDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "titan: %v\n", err)
		return 1
	}
	fmt.Printf("running %d tasks against %s\n\n", len(tasks), provider.Model)

	adapter := buildAdapter(provider)
	workRoot, err := os.MkdirTemp("", "titan-eval-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "titan: %v\n", err)
		return 1
	}
	defer os.RemoveAll(workRoot)

	runner := func(ctx context.Context, ws string, task eval.Task) ([]agent.Event, eval.Result, error) {
		sb, err := buildSandbox(cfg, ws)
		if err != nil {
			return nil, eval.Result{}, err
		}
		sess, err := tools.NewSession(ws)
		if err != nil {
			return nil, eval.Result{}, err
		}
		registry := tools.NewRegistry(
			tools.Read{}, tools.Write{}, tools.Edit{},
			tools.Glob{}, tools.Grep{}, tools.Bash{Sandbox: sb.Command},
		)

		pol := policy.New(policy.ModeAuto)
		must(pol.AddDeny(cfg.Permissions.Deny...))
		must(pol.AddAllow("bash(go *)", "bash(npm *)", "bash(python *)", "bash(cat *)", "bash(ls*)"))

		store := agent.NewMemStore()
		sessionID := "eval-" + task.ID
		rec := agent.NewRecorder(store, sessionID, "")

		loopCfg := agent.DefaultConfig()
		loopCfg.SystemPrompt = agent.BuildSystemPrompt(agent.BuildOptions{
			Profile: "main", Workspace: ws,
			Model: provider.Model, ContextWindow: provider.ContextWindow,
		})
		if task.MaxTurns > 0 {
			loopCfg.MaxTurns = task.MaxTurns
		} else {
			loopCfg.MaxTurns = 30
		}

		loop := agent.NewLoop(adapter, registry, pol, agent.AutoApprove{Yes: true}, sess, rec, loopCfg)
		loop.Compactor = agent.NewCompactor(adapter, loopCfg.CompactAt)

		runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()

		reason, runErr := loop.Run(runCtx, task.Prompt)
		usage := loop.Usage()
		events, _ := store.Events(sessionID)

		return events, eval.Result{
			Turns: usage.Turns, TokensIn: usage.InputTokens, TokensOut: usage.OutputTokens,
			CacheHitRate: usage.CacheHitRate(), Compactions: usage.Compactions,
			Terminal: string(reason),
		}, runErr
	}

	results, err := eval.Run(context.Background(), tasks, workRoot, runner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "titan: %v\n", err)
		return 1
	}

	for _, r := range results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		fmt.Printf("  %-4s %-28s %2d turns  %6d tok\n", status, r.TaskID, r.Turns, r.TokensIn)
		for _, f := range r.Flags {
			marker := "flag"
			if f.Blocking {
				marker = "BLOCK"
			}
			fmt.Printf("       %s %s: %s\n", marker, f.Kind, f.Detail)
		}
		for _, f := range r.Failures {
			fmt.Printf("       %s\n", f)
		}
	}

	summary := eval.Summarize(results, provider.Model)
	fmt.Printf("\n%s", summary.Render())

	if jsonPath != "" {
		data, _ := json.MarshalIndent(summary, "", "  ")
		os.WriteFile(jsonPath, append(data, '\n'), 0o644)
		fmt.Printf("\nreport written to %s\n", jsonPath)
	}

	// A non-zero exit lets CI gate on the eval, which is the point.
	if summary.Passed < summary.Tasks {
		return 1
	}
	return 0
}

// buildAuth constructs the identity layer. OIDC verifies tokens properly;
// proxy mode trusts headers and is only safe behind a trusted proxy.
func buildAuth(cfg config.Config) (*auth.Middleware, error) {
	mw := &auth.Middleware{PublicPaths: []string{"/v1/health", "/"}}
	switch cfg.Auth.Mode {
	case "oidc":
		v, err := auth.NewVerifier(auth.Config{
			Issuer:      cfg.Auth.Issuer,
			Audience:    cfg.Auth.Audience,
			JWKSURL:     cfg.Auth.JWKSURL,
			TenantClaim: cfg.Auth.TenantClaim,
			GroupsClaim: cfg.Auth.GroupsClaim,
		})
		if err != nil {
			return nil, err
		}
		// Fetch the JWKS eagerly so a misconfigured issuer fails at startup
		// rather than on the first user request.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := v.Refresh(ctx); err != nil {
			return nil, fmt.Errorf("OIDC setup failed: %w", err)
		}
		mw.Verifier = v

		// Browser sign-in is optional: an API-only deployment behind a gateway
		// needs no redirect flow.
		if cfg.Auth.ClientID != "" && cfg.Auth.RedirectURL != "" {
			secret := cfg.Auth.ClientSecret
			if secret == "" && cfg.Auth.ClientSecretEnv != "" {
				secret = os.Getenv(cfg.Auth.ClientSecretEnv)
			}
			ttl := time.Duration(cfg.Auth.SessionHours) * time.Hour
			lg, err := auth.NewLogin(auth.LoginConfig{
				Issuer:        cfg.Auth.Issuer,
				ClientID:      cfg.Auth.ClientID,
				ClientSecret:  secret,
				RedirectURL:   cfg.Auth.RedirectURL,
				PostLogoutURL: cfg.Auth.PostLogoutURL,
				Scopes:        cfg.Auth.Scopes,
				Secure:        cfg.Auth.CookieSecure,
				SessionTTL:    ttl,
			}, v)
			if err != nil {
				return nil, fmt.Errorf("browser sign-in setup failed: %w", err)
			}
			mw.Login = lg
		}
	case "proxy":
		mw.TrustHeaders = true
	}
	return mw, nil
}

func authLabel(cfg config.Config) string {
	switch cfg.Auth.Mode {
	case "oidc":
		return "oidc (issuer " + cfg.Auth.Issuer + ")"
	case "proxy":
		return "proxy headers (only safe behind a trusted proxy)"
	default:
		return "none (single-tenant, no authentication)"
	}
}

// recordSession creates the durable session row that events reference.
// A no-op on the memory store, which has no session table.
func recordSession(ctx context.Context, st server.EventStore, id string, cfg config.Config, prompt string) {
	rec, ok := st.(interface {
		CreateSession(context.Context, store.SessionRecord) error
	})
	if !ok {
		return
	}
	user := os.Getenv("USER")
	if user == "" {
		user = "local"
	}
	provider, _ := cfg.Provider()
	tenant := cfg.Storage.Tenant
	if tenant == "" {
		tenant = "default"
	}
	if err := rec.CreateSession(ctx, store.SessionRecord{
		ID: id, Tenant: tenant, User: user,
		Workspace: mustCwd(), Model: provider.Model,
		Mode:      orDefault(cfg.Permissions.Mode, "default"),
		StartedAt: time.Now().UTC(),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "titan: could not persist session: %v\n", err)
	}
}

// lineDelta counts added and removed lines between two versions, for /diff.
func lineDelta(before, after string) (added, removed int) {
	b := strings.Split(before, "\n")
	a := strings.Split(after, "\n")
	counts := map[string]int{}
	for _, line := range b {
		counts[line]++
	}
	for _, line := range a {
		if counts[line] > 0 {
			counts[line]--
		} else {
			added++
		}
	}
	for _, n := range counts {
		removed += n
	}
	return added, removed
}

func mustCwd() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
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

// buildWebSearch constructs the web search tool when enabled. Returns nil, nil
// when the operator has left it off, which is the default.
func buildWebSearch(cfg config.Config) (tools.Tool, error) {
	if !cfg.WebSearch.Enabled {
		return nil, nil
	}
	key := cfg.WebSearch.APIKey
	if key == "" && cfg.WebSearch.APIKeyEnv != "" {
		key = os.Getenv(cfg.WebSearch.APIKeyEnv)
	}
	p, err := websearch.New(websearch.Config{
		Provider:   cfg.WebSearch.Provider,
		APIKey:     key,
		BaseURL:    cfg.WebSearch.BaseURL,
		MaxResults: cfg.WebSearch.MaxResults,
	})
	if err != nil {
		return nil, err
	}
	return &websearch.Tool{Provider: p, Limit: cfg.WebSearch.MaxResults}, nil
}

func webSearchLabel(cfg config.Config) string {
	if !cfg.WebSearch.Enabled {
		return "disabled"
	}
	p := cfg.WebSearch.Provider
	if p == "" {
		p = "duckduckgo"
	}
	return p + " (agent can reach the public internet)"
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

	fmt.Printf("%s %s\n\n", ui.NewStyle(os.Stdout).Cyan(ui.Glyph),
		ui.NewStyle(os.Stdout).Bold("titan doctor"))
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
	fmt.Printf("auth        %s\n", authLabel(cfg))
	if cfg.Auth.Mode == "oidc" {
		if _, err := buildAuth(cfg); err != nil {
			fmt.Printf("            UNAVAILABLE — %v\n", err)
		} else {
			fmt.Printf("            JWKS reachable, tokens will be verified\n")
		}
	}
	fmt.Printf("web search  %s\n", webSearchLabel(cfg))
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
