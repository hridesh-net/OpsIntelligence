package localintel

// DefaultGGUFURL_forTest_setValue swaps DefaultGGUFURL and returns the
// previous value so tests can restore it. Kept in an _test.go file so
// the symbol doesn't leak into production callers.
func DefaultGGUFURL_forTest_setValue(v string) string {
	prev := DefaultGGUFURL
	DefaultGGUFURL = v
	return prev
}
