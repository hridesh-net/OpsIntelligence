//go:build !opsintelligence_localgemma

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func localgemmaCmd(_ *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "localgemma",
		Short: "Run Gemma 4 E2B fully inside the binary (optional build)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf(`localgemma is not available in this build.

To compile in the in-process Gemma engine (llama.cpp via gollama.cpp), rebuild with the extra tag opsintelligence_localgemma, for example:

  go build -tags "fts5,opsintelligence_localgemma" -o opsintelligence ./cmd/opsintelligence

To also embed a GGUF inside the executable, add opsintelligence_embedlocalgemma and place the weights file at:

  internal/localintel/embedded/gemma-4-e2b-it.gguf

before you run go build. Without embedding, set OPSINTELLIGENCE_LOCAL_GEMMA_GGUF to a local .gguf path at runtime.`)
		},
	}
}
