package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuvrajsingh/titan/internal/model"
	"github.com/yuvrajsingh/titan/internal/policy"
	"github.com/yuvrajsingh/titan/internal/tools"
)

// slowTool records concurrency so a test can prove calls actually overlapped.
type slowTool struct {
	name     string
	mutates  bool
	delay    time.Duration
	inFlight int32
	peak     int32
	order    *[]string
	mu       *sync.Mutex
}

func (s *slowTool) Name() string                { return s.name }
func (s *slowTool) Description() string         { return "test tool" }
func (s *slowTool) Schema() json.RawMessage     { return json.RawMessage(`{"type":"object"}`) }
func (s *slowTool) Mutates() bool               { return s.mutates }

func (s *slowTool) Run(ctx context.Context, _ *tools.Session, args json.RawMessage) tools.Result {
	n := atomic.AddInt32(&s.inFlight, 1)
	for {
		peak := atomic.LoadInt32(&s.peak)
		if n <= peak || atomic.CompareAndSwapInt32(&s.peak, peak, n) {
			break
		}
	}
	if s.order != nil {
		s.mu.Lock()
		*s.order = append(*s.order, string(args))
		s.mu.Unlock()
	}
	time.Sleep(s.delay)
	atomic.AddInt32(&s.inFlight, -1)
	return tools.Result{Content: "ok " + string(args)}
}

func parallelHarness(t *testing.T, turns []scriptedTurn, extra ...tools.Tool) (*Loop, *MemStore) {
	t.Helper()
	dir := tempDir(t)
	sess, err := tools.NewSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemStore()
	rec := NewRecorder(store, "sess1", "")
	all := append([]tools.Tool{tools.Read{}, tools.Glob{}}, extra...)
	reg := tools.NewRegistry(all...)
	pol := policy.New(policy.ModeAuto)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pol.AddAllow("*"))
	l := NewLoop(&scriptedAdapter{turns: turns}, reg, pol, AutoApprove{Yes: true}, sess, rec, DefaultConfig())
	return l, store
}

// Independent read-only calls should overlap: four reads is one round trip, not
// four in series.
func TestReadOnlyCallsRunConcurrently(t *testing.T) {
	tool := &slowTool{name: "slow", delay: 60 * time.Millisecond, mu: &sync.Mutex{}}
	var calls []model.ToolCall
	for i := 0; i < 4; i++ {
		calls = append(calls, model.ToolCall{
			ID: fmt.Sprintf("c%d", i), Name: "slow",
			Args: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)),
		})
	}
	loop, _ := parallelHarness(t, []scriptedTurn{{calls: calls}, {text: "done"}}, tool)

	start := time.Now()
	if _, err := loop.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if peak := atomic.LoadInt32(&tool.peak); peak < 2 {
		t.Errorf("peak concurrency = %d; independent calls did not overlap", peak)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("four 60ms calls took %s; they ran in series", elapsed)
	}
}

// A mutating call must never race: two edits to one file, or an edit racing a
// read, give an outcome that depends on scheduling — and a session that cannot
// be replayed to the same result is not auditable.
func TestMutatingCallsNeverOverlap(t *testing.T) {
	tool := &slowTool{name: "mutate", mutates: true, delay: 30 * time.Millisecond, mu: &sync.Mutex{}}
	var calls []model.ToolCall
	for i := 0; i < 3; i++ {
		calls = append(calls, model.ToolCall{
			ID: fmt.Sprintf("m%d", i), Name: "mutate",
			Args: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)),
		})
	}
	loop, _ := parallelHarness(t, []scriptedTurn{{calls: calls}, {text: "done"}}, tool)
	if _, err := loop.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if peak := atomic.LoadInt32(&tool.peak); peak != 1 {
		t.Fatalf("peak concurrency = %d for mutating calls; they must run one at a time", peak)
	}
}

// The model must see results in the order it asked for them, whatever order
// they finished in.
func TestResultsKeepCallOrder(t *testing.T) {
	fast := &slowTool{name: "fast", delay: 1 * time.Millisecond, mu: &sync.Mutex{}}
	slow := &slowTool{name: "slow", delay: 80 * time.Millisecond, mu: &sync.Mutex{}}
	calls := []model.ToolCall{
		{ID: "a", Name: "slow", Args: json.RawMessage(`{"i":"first"}`)},
		{ID: "b", Name: "fast", Args: json.RawMessage(`{"i":"second"}`)},
	}
	loop, _ := parallelHarness(t, []scriptedTurn{{calls: calls}, {text: "done"}}, fast, slow)
	if _, err := loop.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	var ids []string
	for _, m := range loop.Messages() {
		if m.Role == model.RoleTool {
			ids = append(ids, m.ToolCallID)
		}
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("tool results in order %v; the slow call was asked for first and "+
			"must appear first", ids)
	}
}

// Approvals stay sequential: two prompts at once cannot be answered.
func TestApprovalsAreSequential(t *testing.T) {
	var concurrent, peak int32
	appr := approverFunc(func(ctx context.Context, tool string, args json.RawMessage, res policy.Result) (bool, error) {
		n := atomic.AddInt32(&concurrent, 1)
		if n > atomic.LoadInt32(&peak) {
			atomic.StoreInt32(&peak, n)
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&concurrent, -1)
		return true, nil
	})

	tool := &slowTool{name: "asky", mutates: true, delay: time.Millisecond, mu: &sync.Mutex{}}
	dir := tempDir(t)
	sess, _ := tools.NewSession(dir)
	store := NewMemStore()
	rec := NewRecorder(store, "sess1", "")
	reg := tools.NewRegistry(tool)
	// Default mode asks for every mutation.
	loop := NewLoop(&scriptedAdapter{turns: []scriptedTurn{
		{calls: []model.ToolCall{
			{ID: "1", Name: "asky", Args: json.RawMessage(`{}`)},
			{ID: "2", Name: "asky", Args: json.RawMessage(`{}`)},
			{ID: "3", Name: "asky", Args: json.RawMessage(`{}`)},
		}},
		{text: "done"},
	}}, reg, policy.New(policy.ModeDefault), appr, sess, rec, DefaultConfig())

	if _, err := loop.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if p := atomic.LoadInt32(&peak); p != 1 {
		t.Fatalf("%d approval prompts were open at once; a user cannot answer two", p)
	}
}

type approverFunc func(context.Context, string, json.RawMessage, policy.Result) (bool, error)

func (f approverFunc) Approve(ctx context.Context, tool string, args json.RawMessage, res policy.Result) (bool, error) {
	return f(ctx, tool, args, res)
}
