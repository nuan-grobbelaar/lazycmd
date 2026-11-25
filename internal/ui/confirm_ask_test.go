package ui

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"

	"lazycmd/internal/ask"
	"lazycmd/internal/config"
	"lazycmd/internal/jobs"
)

func confirmGroups() []config.Group {
	return []config.Group{{Title: "Global", Commands: []config.ResolvedCommand{
		{ID: "global:deploy", Name: "deploy", Run: "true", Confirm: "Deploy {tag} to {env}?", Params: []config.Param{
			{Name: "env", Type: config.ParamSelect, Options: config.Opts("dev", "prod"), Default: "prod"},
			{Name: "tag", Type: config.ParamString, Default: "v1"},
		}},
		{ID: "global:wipe", Name: "wipe", Run: "true", Confirm: "Run wipe?"},
	}}}
}

func TestConfirmAfterFormAndBack(t *testing.T) {
	m := selectByName(t, ready(t, confirmGroups(), t.TempDir(), t.TempDir()), "deploy")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ}) // run now
	if m.confirm == nil || len(m.jobs) != 0 || m.activeForm != nil {
		t.Fatalf("confirm %v, jobs %d, form %v", m.confirm != nil, len(m.jobs), m.activeForm != nil)
	}
	if v := m.View(); !strings.Contains(v, "Deploy v1 to prod?") {
		t.Errorf("view lacks the question:\n%s", v)
	}

	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // does nothing
	if m.confirm == nil || len(m.jobs) != 0 {
		t.Fatal("enter answered the confirm")
	}

	m, _ = sendKey(t, m, runes("n")) // back to the form, answers kept
	if m.confirm != nil || m.activeForm == nil || m.activeForm.values()["tag"] != "v1" {
		t.Fatalf("after n: confirm %v, form %v", m.confirm != nil, m.activeForm)
	}
	if m.activeForm.form.State != huh.StateNormal {
		t.Error("returned form is still marked completed")
	}

	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	m, _ = sendKey(t, m, runes("y"))
	if m.confirm != nil || len(m.jobs) != 1 || m.jobs[0].values["env"] != "prod" {
		t.Fatalf("after y: confirm %v, jobs %+v", m.confirm != nil, m.jobs)
	}

	// A restart asks again.
	m.focus = focusJobs
	m, _ = sendKey(t, m, runes("r"))
	if m.confirm == nil || len(m.jobs) != 1 {
		t.Error("r restarted without asking")
	}
}

func TestConfirmWithoutFormAndOnRerun(t *testing.T) {
	m := selectByName(t, ready(t, confirmGroups(), t.TempDir(), t.TempDir()), "wipe")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.confirm == nil || m.confirm.form != nil {
		t.Fatal("no confirm for a command without params")
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.confirm != nil || len(m.jobs) != 0 {
		t.Fatal("esc didn't cancel")
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = sendKey(t, m, runes("y"))
	if len(m.jobs) != 1 {
		t.Fatal("y didn't run it")
	}
	m, _ = sendKey(t, m, runes(".")) // run last asks too
	if m.confirm == nil || len(m.jobs) != 1 {
		t.Error(". re-ran without asking")
	}
}

// --- ask popups ---

func askModel(t *testing.T, cmds ...config.ResolvedCommand) (Model, *ask.Server) {
	t.Helper()
	srv, err := ask.NewServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	m := ready(t, []config.Group{{Title: "Global", Commands: cmds}}, t.TempDir(), t.TempDir()).WithAsk(srv)
	return m, srv
}

func startNamed(t *testing.T, m Model, name string) Model {
	t.Helper()
	m = selectByName(t, m, name)
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	return m
}

// pressAndRun sends a key and feeds back whatever messages its commands
// produce right away (huh finishes a form through a follow-up message,
// which the real program would deliver). Slow commands, like a cursor
// blink tick, are left behind.
func pressAndRun(t *testing.T, m Model, k tea.KeyMsg) Model {
	t.Helper()
	next, cmd := m.Update(k)
	m = next.(Model)
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(50 * time.Millisecond):
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			pending = append(pending, batch...)
			continue
		}
		if msg == nil {
			continue
		}
		if _, isPoll := msg.(pollMsg); isPoll {
			continue
		}
		next, c2 := m.Update(msg)
		m = next.(Model)
		pending = append(pending, c2)
	}
	return m
}

// pollUntil sends poll ticks until cond holds.
func pollUntil(t *testing.T, m Model, cond func(Model) bool) Model {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond(m) {
			return m
		}
		next, _ := m.Update(pollMsg{})
		m = next.(Model)
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition never held")
	return m
}

func sendAsk(srv *ask.Server, token string, req ask.Request) chan ask.Response {
	ch := make(chan ask.Response, 1)
	req.Token = token
	go func() {
		r, err := ask.Send(srv.Path(), req)
		if err != nil {
			r = ask.Response{Error: err.Error()}
		}
		ch <- r
	}()
	return ch
}

func waitResp(t *testing.T, ch chan ask.Response) ask.Response {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("no answer reached the script")
		return ask.Response{}
	}
}

func TestJobGetsAskEnv(t *testing.T) {
	m, srv := askModel(t, config.ResolvedCommand{ID: "global:env", Name: "env", Run: `echo "sock=$LC_ASK_SOCK tok=${#LC_ASK_TOKEN}"`})
	m = startNamed(t, m, "env")
	m = pollUntil(t, m, func(m Model) bool {
		out, _ := m.jobs[0].job.Buffer.Snapshot()
		return strings.Contains(out, "tok=")
	})
	out, _ := m.jobs[0].job.Buffer.Snapshot()
	if !strings.Contains(out, "sock="+srv.Path()) || !strings.Contains(out, "tok=32") {
		t.Errorf("job env = %q", out)
	}
}

func TestAskPopupAnswersTheScript(t *testing.T) {
	m, srv := askModel(t, config.ResolvedCommand{ID: "global:slow", Name: "slow", Run: "sleep 10"})
	m = startNamed(t, m, "slow")
	tj := m.jobs[0]
	defer tj.job.Kill()

	ch := sendAsk(srv, tj.askToken, ask.Request{Kind: ask.KindAsk, Prompt: "Name\x1b]0;x\x07?", Default: "d"})
	m = pollUntil(t, m, func(m Model) bool { return m.askPopup != nil })
	if v := m.View(); !strings.Contains(v, "Name^[]0;x^G?") || strings.Contains(v, "\x1b]0;") {
		t.Errorf("prompt not shown cleaned:\n%s", v)
	}
	m, _ = sendKey(t, m, runes("x"))
	m = pressAndRun(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if r := waitResp(t, ch); r.Value != "dx" {
		t.Errorf("answer = %+v, want the default plus what was typed", r)
	}
	if m.askPopup != nil || len(tj.asks) != 0 {
		t.Error("popup or queue not cleared")
	}

	// esc cancels.
	ch = sendAsk(srv, tj.askToken, ask.Request{Kind: ask.KindPick, Prompt: "Which?", Options: []ask.Option{{Value: "/a", Label: "A"}, {Value: "/b"}}})
	m = pollUntil(t, m, func(m Model) bool { return m.askPopup != nil })
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if r := waitResp(t, ch); !r.Cancelled {
		t.Errorf("esc: %+v", r)
	}
}

func TestPickPopupSendsValues(t *testing.T) {
	m, srv := askModel(t, config.ResolvedCommand{ID: "global:slow", Name: "slow", Run: "sleep 10"})
	m = startNamed(t, m, "slow")
	tj := m.jobs[0]
	defer tj.job.Kill()

	ch := sendAsk(srv, tj.askToken, ask.Request{Kind: ask.KindPick, Prompt: "Which?", Options: []ask.Option{{Value: "/r/a", Label: "A"}, {Value: "/r/b", Label: "B"}}})
	m = pollUntil(t, m, func(m Model) bool { return m.askPopup != nil })
	if v := m.View(); !strings.Contains(v, "A") || strings.Contains(v, "/r/a") {
		t.Errorf("pick should show labels:\n%s", v)
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = pressAndRun(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if r := waitResp(t, ch); r.Value != "/r/b" {
		t.Errorf("pick = %+v", r)
	}

	ch = sendAsk(srv, tj.askToken, ask.Request{Kind: ask.KindPick, Multi: true, Prompt: "Which?", Options: []ask.Option{{Value: "a"}, {Value: "b"}}})
	m = pollUntil(t, m, func(m Model) bool { return m.askPopup != nil })
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeySpace}) // tick a
	m = pressAndRun(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if r := waitResp(t, ch); len(r.Values) != 1 || r.Values[0] != "a" {
		t.Errorf("multi pick = %+v", r)
	}
}

func TestAskFromOtherJobWaits(t *testing.T) {
	m, srv := askModel(t,
		config.ResolvedCommand{ID: "global:one", Name: "one", Run: "sleep 10"},
		config.ResolvedCommand{ID: "global:two", Name: "two", Run: "sleep 10"},
	)
	m = startNamed(t, m, "one")
	m = startNamed(t, m, "two") // now the shown job
	one, two := m.jobs[0], m.jobs[1]
	defer one.job.Kill()
	defer two.job.Kill()

	ch := sendAsk(srv, one.askToken, ask.Request{Kind: ask.KindAsk, Prompt: "From one?"})
	m = pollUntil(t, m, func(m Model) bool { return len(one.asks) == 1 })
	m = pollUntil(t, m, func(Model) bool { return true })
	if m.askPopup != nil {
		t.Fatal("a background job's question took focus")
	}
	if !strings.Contains(jobIcon(one, jobs.StatusRunning), "?") {
		t.Error("waiting job isn't marked ?")
	}

	m.switchToJob(one)
	m = pollUntil(t, m, func(m Model) bool { return m.askPopup != nil })
	m, _ = sendKey(t, m, runes("ok"))
	m = pressAndRun(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if r := waitResp(t, ch); r.Value != "ok" {
		t.Errorf("answer = %+v", r)
	}
}

func TestStoppedJobCancelsItsQuestion(t *testing.T) {
	m, srv := askModel(t, config.ResolvedCommand{ID: "global:slow", Name: "slow", Run: "sleep 10"})
	m = startNamed(t, m, "slow")
	tj := m.jobs[0]
	ch := sendAsk(srv, tj.askToken, ask.Request{Kind: ask.KindAsk, Prompt: "?"})
	m = pollUntil(t, m, func(m Model) bool { return m.askPopup != nil })

	tj.job.Kill()
	m = pollUntil(t, m, func(m Model) bool { return m.askPopup == nil })
	if r := waitResp(t, ch); !r.Cancelled {
		t.Errorf("response = %+v, want cancelled", r)
	}
	if tj.askToken != "" {
		t.Error("finished job's token not forgotten")
	}
}

// End to end: a job runs the real `lazycmd ask` binary, the popup opens,
// and the script prints what was answered.
func TestRealBinaryAskInPanel(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := t.TempDir() + "/lazycmd"
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/lazycmd").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	m, _ := askModel(t, config.ResolvedCommand{ID: "global:greet", Name: "greet",
		Run: `name=$("` + bin + `" ask "Your name?") && echo "hello $name" || echo "cancelled $?"`})
	m = startNamed(t, m, "greet")
	m = pollUntil(t, m, func(m Model) bool { return m.askPopup != nil })
	m, _ = sendKey(t, m, runes("nuan"))
	m = pressAndRun(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = pollUntil(t, m, func(m Model) bool {
		out, _ := m.jobs[0].job.Buffer.Snapshot()
		return strings.Contains(out, "hello nuan")
	})
}

// The selected job's band must run the whole row, not stop after the
// coloured status mark.
func TestSelectedJobRowIsOneBand(t *testing.T) {
	m, _ := askModel(t, config.ResolvedCommand{ID: "global:one", Name: "one", Run: "sleep 10"})
	m = startNamed(t, m, "one")
	defer m.jobs[0].job.Kill()
	m.focus, m.jobsCursor = focusJobs, 0

	row := strings.Split(m.renderJobsList(30), "\n")[0]
	_, rest, ok := strings.Cut(ansi.Strip(row), "◐")
	if !ok {
		t.Fatalf("no running mark in %q", ansi.Strip(row))
	}
	if !strings.Contains(row, selectedRowStyle(true).Render(rest)) {
		t.Errorf("name and time aren't on the selection band: %q", row)
	}
}
