package tuibridge

import (
	"strings"
	"testing"
)

func TestStripANSI(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "hello", "hello"},
		{"sgr-truecolor", "\x1b[1;38;2;255;112;67mOPS\x1b[0mINTEL", "OPSINTEL"},
		{"per-letter gradient", "\x1b[38;2;255;112;67mO\x1b[38;2;208;89;46mP\x1b[0mS", "OPS"},
		{"background runs", "\x1b[48;2;30;30;30m   padded   \x1b[0m", "   padded   "},
		{"osc-bel hyperlink", "\x1b]8;;https://x\x07link\x1b]8;;\x07", "link"},
		{"osc-st title", "\x1b]0;title\x1b\\after", "after"},
		{"charset", "\x1b(Btext", "text"},
		{"cursor", "\x1b[2Jcleared", "cleared"},
		{"multiline keeps newlines", "\x1b[31mred\x1b[0m\nplain", "red\nplain"},
		{"dangling esc at end", "abc\x1b", "abc"},
	}
	for _, c := range cases {
		if got := StripANSI(c.in); got != c.want {
			t.Errorf("%s: StripANSI(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestStripANSI_NoEscapeFragmentsSurvive(t *testing.T) {
	// The v1.0.87 REPL bug: lipgloss banner text reached the Rust TUI and
	// rendered fragments like "[1;38;2;255;112;67m". After stripping, no
	// SGR-looking residue may remain.
	in := "\x1b[1;38;2;255;112;67mN\x1b[1;38;2;208;89;46mT\x1b[0mEL v1.0.87"
	got := StripANSI(in)
	if strings.Contains(got, "[1;38") || strings.Contains(got, "m") && strings.Contains(got, ";") {
		t.Fatalf("escape residue survived: %q", got)
	}
	if got != "NTEL v1.0.87" {
		t.Fatalf("got %q, want %q", got, "NTEL v1.0.87")
	}
}
