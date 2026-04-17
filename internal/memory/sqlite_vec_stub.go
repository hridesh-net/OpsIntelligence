//go:build !cgo

package memory

import (
	"fmt"
	"os"
)

// AutoRegisterVec is a no-op stub for when CGO is disabled.
func AutoRegisterVec() {
	fmt.Fprintf(os.Stderr, "[opsintelligence] WARNING: sqlite-vec auto-registration skipped (CGO disabled)\n")
}
