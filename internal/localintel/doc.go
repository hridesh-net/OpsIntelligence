// Package localintel hosts OpsIntelligence's optional on-device Gemma 4 E2B stack.
//
// Build modes:
//   • Default: no embedded weights, no in-process engine — APIs return ErrNotCompiled or no-op.
//   • -tags opsintelligence_localgemma — link the llama.cpp-backed engine (purego/FFI via gollama.cpp).
//   • -tags opsintelligence_embedlocalgemma — embed GGUF bytes at internal/localintel/embedded/gemma-4-e2b-it.gguf
//     (place that file before building; it is gitignored).
//
// Typical release build for a single binary with weights inside the executable:
//
//	go build -tags "fts5,opsintelligence_localgemma,opsintelligence_embedlocalgemma" -o opsintelligence ./cmd/opsintelligence
//
// For development without embedding, point OPSINTELLIGENCE_LOCAL_GEMMA_GGUF at a local .gguf file and use
// opsintelligence_localgemma only.
//
// When wired through the agent runner, enable opsintelligence.yaml → agent.local_intel.enabled and supply
// gguf_path or embedded weights as documented above.
package localintel
