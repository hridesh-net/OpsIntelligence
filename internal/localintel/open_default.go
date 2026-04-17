//go:build !opsintelligence_localgemma

package localintel

// CompiledWithLocalGemma reports whether the binary was built with opsintelligence_localgemma (in-process llama.cpp).
func CompiledWithLocalGemma() bool { return false }

func openEngine(_ Options) (Engine, error) {
	return noopEngine{}, nil
}
