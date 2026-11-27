package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lazycmd/internal/config"
)

func inlineKey(m *inlineForm, s string) {
	var msg tea.KeyMsg
	switch s {
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "alt+enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	m.Update(msg)
}

func TestInlineFormConfirmFlow(t *testing.T) {
	cmd := config.ResolvedCommand{Name: "deploy", ScriptPath: "x.sh", Confirm: "Deploy {env}?",
		Params: []config.Param{{Name: "env", Type: config.ParamString, Default: "prod"}}}
	m := &inlineForm{pf: newParamsForm(cmd, t.TempDir())}
	m.Init()

	inlineKey(m, "alt+enter") // run now: valid, so on to the question
	if !m.confirming || m.done {
		t.Fatalf("want the confirm question, got confirming=%v done=%v", m.confirming, m.done)
	}
	inlineKey(m, "n") // back to the form, answers kept
	if m.confirming || m.quitting {
		t.Fatal("n should go back to the form")
	}
	inlineKey(m, "alt+enter")
	inlineKey(m, "y")
	if !m.done || !m.quitting || m.pf.values()["env"] != "prod" {
		t.Errorf("done=%v values=%v", m.done, m.pf.values())
	}
}

func TestInlineFormRunNowRefusedWhenRequiredEmpty(t *testing.T) {
	cmd := config.ResolvedCommand{Name: "d", ScriptPath: "x.sh",
		Params: []config.Param{{Name: "tag", Type: config.ParamString, Required: true}}}
	m := &inlineForm{pf: newParamsForm(cmd, t.TempDir())}
	m.Init()
	inlineKey(m, "alt+enter")
	if m.quitting || m.pf.notice == "" {
		t.Errorf("run now should be refused with a notice; quitting=%v notice=%q", m.quitting, m.pf.notice)
	}
}

func TestInlineFormNoParamsCancel(t *testing.T) {
	cmd := config.ResolvedCommand{Name: "d", ScriptPath: "x.sh", Confirm: "Sure?"}
	m := &inlineForm{pf: inlineParams(cmd, t.TempDir()), confirming: true}
	inlineKey(m, "esc")
	if !m.quitting || m.done {
		t.Error("esc on a question with no form should cancel")
	}
}

func TestFormProblemsFirstOption(t *testing.T) {
	cmd := config.ResolvedCommand{Name: "d", ScriptPath: "x.sh", Params: []config.Param{
		{Name: "env", Type: config.ParamSelect, Options: config.Opts("a", "b")},
		{Name: "tag", Type: config.ParamString, Required: true},
	}}
	problems, values := FormProblems(cmd, t.TempDir())
	if len(problems) != 1 || values["env"] != "a" {
		t.Errorf("problems=%v values=%v", problems, values)
	}
}
