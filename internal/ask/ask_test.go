package ask

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// next waits for the next question to reach the "UI".
func next(t *testing.T, s *Server) *Pending {
	t.Helper()
	select {
	case p := <-s.Requests:
		return p
	case <-time.After(3 * time.Second):
		t.Fatal("no question arrived")
		return nil
	}
}

func TestRoundTrip(t *testing.T) {
	s := newServer(t)
	tok := s.Register("deploy-1")
	got := make(chan Response, 1)
	go func() {
		r, err := Send(s.Path(), Request{Token: tok, Kind: KindAsk, Prompt: "Name?", Default: "x"})
		if err != nil {
			t.Error(err)
		}
		got <- r
	}()
	p := next(t, s)
	if p.JobID != "deploy-1" || p.Req.Prompt != "Name?" || p.Req.Default != "x" {
		t.Errorf("pending = %+v", p)
	}
	p.Answer(Response{Value: "v1"})
	p.Answer(Response{Value: "ignored"}) // only the first answer counts
	if r := <-got; r.Value != "v1" {
		t.Errorf("response = %+v", r)
	}
}

func TestRejectsUnknownTokenAndBadRequests(t *testing.T) {
	s := newServer(t)
	tok := s.Register("j")
	cases := map[string]Request{
		"unknown token":      {Token: "nope", Kind: KindAsk, Prompt: "?"},
		"pick no options":    {Token: tok, Kind: KindPick, Prompt: "?"},
		"secret pick":        {Token: tok, Kind: KindPick, Prompt: "?", Secret: true, Options: []Option{{Value: "a"}}},
		"ask with options":   {Token: tok, Kind: KindAsk, Prompt: "?", Options: []Option{{Value: "a"}}},
		"unknown kind":       {Token: tok, Kind: "rm", Prompt: "?"},
		"unregistered later": {Token: "", Kind: KindAsk, Prompt: "?"},
	}
	for name, req := range cases {
		r, err := Send(s.Path(), req)
		if err != nil || r.Error == "" {
			t.Errorf("%s: response %+v, err %v; want an error", name, r, err)
		}
	}
	s.Unregister(tok)
	if r, _ := Send(s.Path(), Request{Token: tok, Kind: KindAsk, Prompt: "?"}); !strings.Contains(r.Error, "unknown token") {
		t.Errorf("after Unregister: %+v", r)
	}
	select {
	case p := <-s.Requests:
		t.Errorf("a rejected request reached the UI: %+v", p)
	default:
	}
}

func TestClientHangUpIsNoticed(t *testing.T) {
	s := newServer(t)
	tok := s.Register("j")
	conn := dialRaw(t, s.Path(), `{"token":"`+tok+`","kind":"ask","prompt":"?"}`+"\n")
	p := next(t, s)
	if p.Gone() {
		t.Fatal("gone before hanging up")
	}
	conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for !p.Gone() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !p.Gone() {
		t.Error("hang-up not noticed")
	}
	p.Answer(Response{Value: "late"}) // must not block
}

func TestSocketFolderIsPrivateAndRemoved(t *testing.T) {
	s, err := NewServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("socket folder mode = %v", info.Mode().Perm())
	}
	s.Close()
	if _, err := os.Stat(filepath.Dir(s.Path())); !os.IsNotExist(err) {
		t.Error("socket folder left behind")
	}
}

func TestLongBaseDirFallsBackToTemp(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 90))
	s, err := NewServer(long)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.Path()) > 104 {
		t.Errorf("socket path too long: %d", len(s.Path()))
	}
	if _, err := Send(s.Path(), Request{Kind: KindAsk, Prompt: "?"}); err != nil {
		t.Errorf("can't reach the fallback socket: %v", err)
	}
}

// --- CLI ---

func runMain(t *testing.T, kind string, args []string, stdin string, tty bool) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(kind, args, strings.NewReader(stdin), tty, &out, &errb)
	return code, out.String(), errb.String()
}

func TestMainInsideLazycmd(t *testing.T) {
	s := newServer(t)
	t.Setenv(EnvSock, s.Path())
	t.Setenv(EnvToken, s.Register("j"))

	type result struct {
		code     int
		out, err string
	}
	run := func(kind string, args []string, stdin string) chan result {
		ch := make(chan result, 1)
		go func() {
			c, o, e := runMain(t, kind, args, stdin, false)
			ch <- result{c, o, e}
		}()
		return ch
	}

	ch := run(KindPick, []string{"Module?", "--multi"}, "/r/api\tapi\n\n/r/web\n")
	p := next(t, s)
	if !p.Req.Multi || len(p.Req.Options) != 2 || p.Req.Options[0] != (Option{Value: "/r/api", Label: "api"}) {
		t.Fatalf("request = %+v", p.Req)
	}
	p.Answer(Response{Values: []string{"/r/api", "/r/web"}})
	if r := <-ch; r.code != ExitOK || r.out != "/r/api\n/r/web\n" {
		t.Errorf("multi pick = %+v", r)
	}

	ch = run(KindAsk, []string{"--secret", "Token?"}, "")
	p = next(t, s)
	if !p.Req.Secret || p.Req.Prompt != "Token?" {
		t.Errorf("request = %+v", p.Req)
	}
	p.Answer(Response{Cancelled: true})
	if r := <-ch; r.code != ExitCancelled || r.out != "" {
		t.Errorf("cancelled ask = %+v", r)
	}
}

func TestMainUsageErrors(t *testing.T) {
	t.Setenv(EnvSock, "")
	cases := []struct {
		kind  string
		args  []string
		stdin string
		tty   bool
	}{
		{KindAsk, nil, "", false},
		{KindAsk, []string{"a", "b"}, "", false},
		{KindAsk, []string{"Q?", "--multi"}, "", false},
		{KindAsk, []string{"Q?", "--default"}, "", false},
		{KindPick, []string{"Q?", "--secret"}, "a\n", false},
		{KindPick, []string{"Q?"}, "", true}, // nothing piped in
	}
	for _, c := range cases {
		if code, _, errOut := runMain(t, c.kind, c.args, c.stdin, c.tty); code != ExitUsage || !strings.Contains(errOut, "usage") {
			t.Errorf("%s %v: code %d, stderr %q", c.kind, c.args, code, errOut)
		}
	}
	if code, _, _ := runMain(t, KindPick, []string{"Q?"}, "\n\n", false); code != ExitCancelled {
		t.Errorf("pick with no options: code %d, want 1", code)
	}
}

// fakeTTY is a terminal for the fallback tests: reads come from in,
// writes go to out.
type fakeTTY struct {
	in  io.Reader
	out bytes.Buffer
}

func (f *fakeTTY) Read(p []byte) (int, error)  { return f.in.Read(p) }
func (f *fakeTTY) Write(p []byte) (int, error) { return f.out.Write(p) }
func (f *fakeTTY) Close() error                { return nil }

func withTTY(t *testing.T, input string) *fakeTTY {
	t.Helper()
	tty := &fakeTTY{in: strings.NewReader(input)}
	old, oldFzf := openTTY, lookFzf
	openTTY = func() (io.ReadWriteCloser, error) { return tty, nil }
	lookFzf = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { openTTY, lookFzf = old, oldFzf })
	t.Setenv(EnvSock, "")
	return tty
}

func TestFallbackAskOnTerminal(t *testing.T) {
	tty := withTTY(t, "\n")
	code, out, _ := runMain(t, KindAsk, []string{"Name?", "--default", "today"}, "", true)
	if code != ExitOK || out != "today\n" || !strings.Contains(tty.out.String(), "Name? [today]") {
		t.Errorf("empty answer: code %d out %q tty %q", code, out, tty.out.String())
	}

	withTTY(t, "typed\n")
	if _, out, _ := runMain(t, KindAsk, []string{"Name?"}, "", true); out != "typed\n" {
		t.Errorf("typed answer = %q", out)
	}

	withTTY(t, "") // EOF: ctrl-d
	if code, _, _ := runMain(t, KindAsk, []string{"Name?"}, "", true); code != ExitCancelled {
		t.Errorf("EOF: code %d, want 1", code)
	}

	tty = withTTY(t, "")
	oldSecret := readSecret
	readSecret = func(io.ReadWriteCloser) (string, error) { return "s3cret", nil }
	defer func() { readSecret = oldSecret }()
	if _, out, _ := runMain(t, KindAsk, []string{"Token?", "--secret", "--default", "nope"}, "", true); out != "s3cret\n" || strings.Contains(tty.out.String(), "nope") {
		t.Errorf("secret: out %q, tty %q (a secret's default must not be shown)", out, tty.out.String())
	}
}

func TestFallbackPickMenu(t *testing.T) {
	tty := withTTY(t, "9\n2\n")
	code, out, _ := runMain(t, KindPick, []string{"Which?"}, "/r/a\tA\n/r/b\tB\n", false)
	if code != ExitOK || out != "/r/b\n" {
		t.Errorf("code %d out %q", code, out)
	}
	if s := tty.out.String(); !strings.Contains(s, "  1) A") || !strings.Contains(s, "not a valid choice") {
		t.Errorf("menu = %q", s)
	}
	withTTY(t, "1 2\n")
	if _, out, _ := runMain(t, KindPick, []string{"Which?", "--multi"}, "a\nb\n", false); out != "a\nb\n" {
		t.Errorf("multi = %q", out)
	}
	withTTY(t, "\n")
	if code, _, _ := runMain(t, KindPick, []string{"Which?"}, "a\n", false); code != ExitCancelled {
		t.Errorf("empty answer: code %d", code)
	}
}

func TestFallbackPickUsesFzf(t *testing.T) {
	dir := t.TempDir()
	fzf := filepath.Join(dir, "fzf")
	// A stand-in fzf: records its arguments and picks the second line.
	script := "#!/bin/sh\necho \"$@\" > " + filepath.Join(dir, "args") + "\nsed -n 2p\n"
	if err := os.WriteFile(fzf, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	withTTY(t, "")
	lookFzf = func() (string, error) { return fzf, nil }

	code, out, _ := runMain(t, KindPick, []string{"Which?"}, "/r/a\tA\n/r/b\n", false)
	if code != ExitOK || out != "/r/b\n" {
		t.Errorf("code %d out %q, want the value of the picked line", code, out)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if !strings.Contains(string(args), "--with-nth 2..") || !strings.Contains(string(args), "--prompt Which?") {
		t.Errorf("fzf args = %q", args)
	}
}

func dialRaw(t *testing.T, sock, line string) io.Closer {
	t.Helper()
	conn, err := netDial(sock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
	return conn
}
