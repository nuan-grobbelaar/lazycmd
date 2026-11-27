package safetext

import (
	"strings"
	"testing"
)

func TestLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"deploy", "deploy"},
		{"npm: test:unit", "npm: test:unit"},
		{"héllo wörld", "héllo wörld"},
		{"main\x1b]52;c;cHduZWQ=\x07", "main^[]52;c;cHduZWQ=^G"},
		{"ok\x1b[8mhidden", "ok^[[8mhidden"},
		{"a\nb\tc\rd", "a b c d"},
		{"del\x7f", "del^?"},
		{"c1\u009b31m", "c1<U+009B>31m"},
		{"raw\x9b31m", "raw�31m"},
		{"evil\u202eexe.txt", "evil<U+202E>exe.txt"},
	}
	for _, c := range cases {
		if got := Line(c.in); got != c.want {
			t.Errorf("Line(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTextKeepsNewlines(t *testing.T) {
	got := Text("one\ntwo\x1b]0;x\x07")
	if got != "one\ntwo^[]0;x^G" {
		t.Errorf("Text = %q", got)
	}
}

func TestNoEscapeSurvives(t *testing.T) {
	var all strings.Builder
	for c := 0; c < 256; c++ {
		all.WriteByte(byte(c))
	}
	for _, out := range []string{Line(all.String()), Text(all.String())} {
		for i := 0; i < len(out); i++ {
			if c := out[i]; (c < 0x20 && c != '\n') || c == 0x7f {
				t.Fatalf("control byte %#x survived in %q", c, out)
			}
		}
		if strings.ContainsRune(out, 0x9b) || strings.ContainsRune(out, 0x9d) {
			t.Fatalf("C1 control survived in %q", out)
		}
	}
}
