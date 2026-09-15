package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// A real SSH server, so the transport, auth and host key verification are
// exercised rather than mocked. Everything below runs against it.
type testServer struct {
	addr    string
	hostKey ssh.PublicKey
	stop    func()
}

func startSSHServer(t *testing.T, password string) *testServer {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "tester" && string(pass) == password {
				return nil, nil
			}
			return nil, fmt.Errorf("denied")
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					return
				}
			}
			go serveConn(conn, cfg)
		}
	}()

	return &testServer{
		addr: ln.Addr().String(), hostKey: signer.PublicKey(),
		stop: func() { close(done); _ = ln.Close() },
	}
}

// serveConn answers exec requests with a canned result, which is all the tool
// needs to be exercised end to end.
func serveConn(nConn net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nConn, cfg)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			return
		}
		go func(ch ssh.Channel, in <-chan *ssh.Request) {
			defer func() { _ = ch.Close() }()
			for req := range in {
				if req.Type != "exec" {
					_ = req.Reply(false, nil)
					continue
				}
				var payload struct{ Command string }
				_ = ssh.Unmarshal(req.Payload, &payload)
				_ = req.Reply(true, nil)

				status := 0
				switch {
				case strings.Contains(payload.Command, "false"):
					_, _ = fmt.Fprint(ch.Stderr(), "it failed\n")
					status = 3
				case strings.Contains(payload.Command, "hostname"):
					_, _ = fmt.Fprint(ch, "abhed-test-vm\n")
				default:
					_, _ = fmt.Fprintf(ch, "ran: %s\n", payload.Command)
				}
				_, _ = ch.SendRequest("exit-status", false,
					ssh.Marshal(struct{ Status uint32 }{uint32(status)}))
				return
			}
		}(ch, requests)
	}
}

// knownHostsFor writes a known_hosts pinning the test server, so host key
// verification is genuinely exercised rather than skipped.
func knownHostsFor(t *testing.T, s *testServer) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	line := fmt.Sprintf("[%s]:%s %s\n",
		strings.Split(s.addr, ":")[0], strings.Split(s.addr, ":")[1],
		strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.hostKey))))
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunAgainstRealSSHServer(t *testing.T) {
	srv := startSSHServer(t, "hunter2")
	defer srv.stop()

	t.Setenv("TEST_SSH_PW", "hunter2")
	reg, errs := NewRegistry([]HostConfig{{
		Name: "vm1", Addr: srv.addr, User: "tester",
		PasswordEnv:    "TEST_SSH_PW",
		KnownHostsFile: knownHostsFor(t, srv),
	}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	defer reg.Close()

	args, _ := json.Marshal(map[string]any{"host": "vm1", "command": "hostname"})
	res := Tool{R: reg}.Run(context.Background(), nil, args)

	if res.IsError {
		t.Fatalf("command failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "abhed-test-vm") {
		t.Errorf("stdout missing: %s", res.Content)
	}
	if !strings.Contains(res.Content, "tester@vm1") {
		t.Errorf("result does not say where it ran: %s", res.Content)
	}
}

// A non-zero exit is a result the model must see, not a transport failure.
func TestNonZeroExitIsReported(t *testing.T) {
	srv := startSSHServer(t, "pw")
	defer srv.stop()
	t.Setenv("TEST_SSH_PW", "pw")

	reg, _ := NewRegistry([]HostConfig{{
		Name: "vm1", Addr: srv.addr, User: "tester",
		PasswordEnv: "TEST_SSH_PW", KnownHostsFile: knownHostsFor(t, srv),
	}})
	defer reg.Close()

	args, _ := json.Marshal(map[string]any{"host": "vm1", "command": "false"})
	res := Tool{R: reg}.Run(context.Background(), nil, args)

	if res.ExitCode == nil || *res.ExitCode != 3 {
		t.Errorf("exit code = %v, want 3", res.ExitCode)
	}
	if !strings.Contains(res.Content, "it failed") {
		t.Errorf("stderr lost: %s", res.Content)
	}
}

// The point of host key verification: a server whose key is not pinned must
// be refused, not silently trusted.
func TestUnknownHostKeyIsRefused(t *testing.T) {
	srv := startSSHServer(t, "pw")
	defer srv.stop()
	t.Setenv("TEST_SSH_PW", "pw")

	empty := filepath.Join(t.TempDir(), "known_hosts")
	_ = os.WriteFile(empty, []byte(""), 0o600)

	reg, _ := NewRegistry([]HostConfig{{
		Name: "vm1", Addr: srv.addr, User: "tester",
		PasswordEnv: "TEST_SSH_PW", KnownHostsFile: empty,
	}})
	defer reg.Close()

	args, _ := json.Marshal(map[string]any{"host": "vm1", "command": "hostname"})
	res := Tool{R: reg}.Run(context.Background(), nil, args)

	if !res.IsError {
		t.Fatal("connected to a host whose key was not pinned")
	}
	if !strings.Contains(res.Content, "known_hosts") {
		t.Errorf("refusal does not explain the fix: %s", res.Content)
	}
}

func TestWrongPasswordIsReported(t *testing.T) {
	srv := startSSHServer(t, "correct")
	defer srv.stop()
	t.Setenv("TEST_SSH_PW", "wrong")

	reg, _ := NewRegistry([]HostConfig{{
		Name: "vm1", Addr: srv.addr, User: "tester",
		PasswordEnv: "TEST_SSH_PW", KnownHostsFile: knownHostsFor(t, srv),
	}})
	defer reg.Close()

	args, _ := json.Marshal(map[string]any{"host": "vm1", "command": "hostname"})
	res := Tool{R: reg}.Run(context.Background(), nil, args)

	if !res.IsError || !strings.Contains(res.Content, "authentication") {
		t.Errorf("auth failure unclear: %s", res.Content)
	}
}

// The connection is reused, so a multi-step task does not re-handshake per
// command.
func TestConnectionIsReused(t *testing.T) {
	srv := startSSHServer(t, "pw")
	defer srv.stop()
	t.Setenv("TEST_SSH_PW", "pw")

	h, err := NewHost(HostConfig{
		Name: "vm1", Addr: srv.addr, User: "tester",
		PasswordEnv: "TEST_SSH_PW", KnownHostsFile: knownHostsFor(t, srv),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()

	ctx := context.Background()
	if _, err := h.Run(ctx, "hostname", 5*time.Second); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := h.client
	if _, err := h.Run(ctx, "hostname", 5*time.Second); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if h.client != first {
		t.Error("reconnected instead of reusing the open connection")
	}
}

// A user pasting "key is at ~Downloads/key (1).prv" — missing slash, misspelled
// directory, a space in the name — should not send the agent hunting with
// glob through directories the sandbox denies.
func TestResolveKeyPathHandlesTypedPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	dl := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(dl, 0o755); err != nil {
		t.Skip("cannot create Downloads")
	}
	name := "abhed-test-key (1).prv"
	real := filepath.Join(dl, name)
	if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
		t.Skip("cannot write test key")
	}
	defer func() { _ = os.Remove(real) }()

	for _, typed := range []string{
		real,                  // exact
		"~/Downloads/" + name, // tilde
		"~Downloads/" + name,  // missing slash AND misspelled, as reported
		name,                  // bare filename
	} {
		got, err := resolveKeyPath(typed)
		if err != nil {
			t.Errorf("resolveKeyPath(%q) failed: %v", typed, err)
			continue
		}
		if got != real {
			t.Errorf("resolveKeyPath(%q) = %q, want %q", typed, got, real)
		}
	}
}

// A path that genuinely does not exist must say where it looked, so the user
// can correct it rather than the agent guessing again.
func TestResolveKeyPathExplainsFailure(t *testing.T) {
	_, err := resolveKeyPath("~/nowhere/definitely-not-a-key-xyz.prv")
	if err == nil {
		t.Fatal("accepted a path that does not exist")
	}
	if !strings.Contains(err.Error(), "Tried:") {
		t.Errorf("error does not say where it looked: %v", err)
	}
}

// ssh_connect must verify before registering: a host stored but unreachable
// turns one clear failure into a confusing one on the next command.
func TestConnectVerifiesBeforeRegistering(t *testing.T) {
	reg, _ := NewRegistry(nil)
	args, _ := json.Marshal(map[string]any{
		"addr": "127.0.0.1:1", "user": "nobody", "accept_host_key": true})
	res := ConnectTool{R: reg}.Run(context.Background(), nil, args)

	if !res.IsError {
		t.Fatal("registered a host it could not reach")
	}
	if reg.Len() != 0 {
		t.Error("an unreachable host was registered anyway")
	}
}

func TestConnectRegistersWorkingHost(t *testing.T) {
	srv := startSSHServer(t, "pw")
	defer srv.stop()
	t.Setenv("TEST_SSH_PW", "pw")

	reg, _ := NewRegistry(nil)
	defer reg.Close()

	host, port, _ := net.SplitHostPort(srv.addr)
	args, _ := json.Marshal(map[string]any{
		"addr": host + ":" + port, "user": "tester", "name": "vm1",
		"password_env": "TEST_SSH_PW", "accept_host_key": true})
	res := ConnectTool{R: reg}.Run(context.Background(), nil, args)

	if res.IsError {
		t.Fatalf("connect failed: %s", res.Content)
	}
	if reg.Len() != 1 {
		t.Fatalf("host not registered: %v", reg.names())
	}
	if !strings.Contains(res.Content, "not written to ~/.ssh/config") {
		t.Errorf("does not say where the credential lives: %s", res.Content)
	}

	// And the ssh tool can now use it.
	runArgs, _ := json.Marshal(map[string]any{"host": "vm1", "command": "hostname"})
	run := Tool{R: reg}.Run(context.Background(), nil, runArgs)
	if run.IsError {
		t.Fatalf("registered host is not usable: %s", run.Content)
	}
}

// Declaring a host changes which machines the agent can reach.
func TestConnectRequiresApproval(t *testing.T) {
	if !(ConnectTool{}).Mutates() {
		t.Error("ssh_connect does not declare itself mutating, so it could run unapproved")
	}
}

// The refusal must tell the model what to do, or it retries identically.
func TestUnknownHostKeyErrorNamesTheRetry(t *testing.T) {
	srv := startSSHServer(t, "pw")
	defer srv.stop()
	t.Setenv("TEST_SSH_PW", "pw")

	empty := filepath.Join(t.TempDir(), "known_hosts")
	_ = os.WriteFile(empty, []byte(""), 0o600)
	h, _ := NewHost(HostConfig{Name: "vm1", Addr: srv.addr, User: "tester",
		PasswordEnv: "TEST_SSH_PW", KnownHostsFile: empty})
	defer func() { _ = h.Close() }()

	_, err := h.Run(context.Background(), "hostname", 5*time.Second)
	if err == nil {
		t.Fatal("connected without a pinned host key")
	}
	if !strings.Contains(err.Error(), "accept_host_key") {
		t.Errorf("error does not name the retry option: %v", err)
	}
}
