package localintel

import "errors"

var (
	// ErrNotCompiled is returned when a feature was requested but this binary was not built with opsintelligence_localgemma.
	ErrNotCompiled = errors.New("localintel: Gemma engine not compiled in (missing go build tag opsintelligence_localgemma)")

	// ErrNoWeights is returned when neither embedded weights nor GGUFPath/OPSINTELLIGENCE_LOCAL_GEMMA_GGUF is available.
	ErrNoWeights = errors.New("localintel: no Gemma GGUF available (embed with opsintelligence_embedlocalgemma or set OPSINTELLIGENCE_LOCAL_GEMMA_GGUF)")

	// ErrEmbeddedWeightsEmpty is returned when the embed tag was set but the embedded byte slice is empty.
	ErrEmbeddedWeightsEmpty = errors.New("localintel: embedded Gemma weights are empty; place a valid GGUF before building with opsintelligence_embedlocalgemma")
)
