//go:build !opsintelligence_localgemma

package localintel

import "testing"

func TestCompiledWithLocalGemma_defaultBinary(t *testing.T) {
	if CompiledWithLocalGemma() {
		t.Fatal("expected CompiledWithLocalGemma false without opsintelligence_localgemma build tag")
	}
}
