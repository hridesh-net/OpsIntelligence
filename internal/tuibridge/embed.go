package tuibridge

import (
	"embed"
	"fmt"
	"io/fs"
	"runtime"
)

//go:embed all:assets
var embeddedAssets embed.FS

// assetName returns the embedded filename expected for the current platform.
func assetName() string {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	return fmt.Sprintf("opsintel-tui-%s-%s%s", runtime.GOOS, runtime.GOARCH, suffix)
}

// readEmbeddedBinary returns the embedded Rust TUI binary bytes for the current
// platform, or an error if no embedded asset is available (typical during local
// development before `make tui` has run).
func readEmbeddedBinary() ([]byte, error) {
	name := assetName()
	data, err := fs.ReadFile(embeddedAssets, "assets/"+name)
	if err != nil {
		return nil, fmt.Errorf("embedded Rust TUI binary %q not found (run `make tui` or use --tui-dev-binary): %w", name, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("embedded Rust TUI binary %q is empty", name)
	}
	return data, nil
}
