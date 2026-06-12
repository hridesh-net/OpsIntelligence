package tuibridge

import "strings"

// StripANSI removes ANSI escape sequences (CSI color/cursor codes, OSC
// titles/hyperlinks, charset selectors) from s. Text crossing the JSON-RPC
// protocol into the Rust TUI is rendered literally by ratatui — any escape
// codes show up as garbage like "[1;38;2;255;112;67m" — so every styled
// string (lipgloss output in particular) must pass through here first.
func StripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		if c != 0x1b {
			b.WriteByte(c)
			i++
			continue
		}
		i++ // consume ESC
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[': // CSI … final byte 0x40–0x7E
			i++
			for i < len(s) {
				fin := s[i] >= 0x40 && s[i] <= 0x7e
				i++
				if fin {
					break
				}
			}
		case ']': // OSC … BEL or ESC \
			i++
			for i < len(s) {
				if s[i] == 0x07 {
					i++
					break
				}
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		case '(', ')': // charset selection: ESC ( B etc. — one more byte
			i += 2
		default: // two-byte escape (ESC c, ESC 7, …)
			i++
		}
	}
	return b.String()
}
