package dispatcher

// cli_drivers.go — concrete configurations of GenericLineDriver for every
// agent CLI that just needs `<bin> <prompt>` to do its thing. One file
// instead of one-per-driver to keep boilerplate minimal; the registry in
// gateway_auth.go imports each NewXxxDriver constructor.
//
// Constructors here mirror the kanbots.dev / leodavinci1/kanbots roster:
//   gemini, cursor-agent, gh-copilot, opencode, amp, qwen, droid, ccr.
//
// Stream-JSON-capable CLIs (claude-code) and the in-process Go runner are
// kept separate. ACP-protocol CLIs are handled by ACPDriver.

// NewGeminiDriver returns a driver for Google's Gemini CLI.
// Auth: `gemini auth login` before first use.
func NewGeminiDriver() *GenericLineDriver {
	return &GenericLineDriver{
		TypeID: "gemini",
		Models: []string{
			"gemini-2.5-flash",
			"gemini-2.5-pro",
			"gemini-1.5-pro",
			"gemini-1.5-flash",
		},
		Binary: "gemini",
		ArgsFn: func(opts RunOpts) []string {
			// Gemini CLI uses `gemini chat --model <m> <prompt>`. Empty
			// model falls through to the CLI's default.
			a := []string{"chat"}
			if opts.Model != "" {
				a = append(a, "--model", opts.Model)
			}
			a = append(a, opts.Prompt)
			return a
		},
	}
}

// NewCursorDriver returns a driver for the Cursor agent CLI.
// Auth: `cursor-agent login`.
func NewCursorDriver() *GenericLineDriver {
	return &GenericLineDriver{
		TypeID: "cursor-agent",
		Models: []string{"claude-sonnet-4-5", "gpt-5", "auto"},
		Binary: "cursor-agent",
		ArgsFn: func(opts RunOpts) []string {
			a := []string{"run"}
			if opts.Model != "" {
				a = append(a, "--model", opts.Model)
			}
			a = append(a, opts.Prompt)
			return a
		},
	}
}

// NewCopilotDriver returns a driver for the GitHub Copilot CLI.
// Auth: `gh auth login` + a Copilot subscription.
func NewCopilotDriver() *GenericLineDriver {
	return &GenericLineDriver{
		TypeID: "gh-copilot",
		Models: []string{"copilot"},
		Binary: "gh",
		ArgsFn: func(opts RunOpts) []string {
			// `gh copilot suggest "<prompt>"` returns a single suggestion.
			// Operators who want agentic loops should use Claude Code /
			// Codex instead; this driver is here for quick one-shots.
			return []string{"copilot", "suggest", opts.Prompt}
		},
	}
}

// NewOpenCodeDriver returns a driver for the OpenCode CLI.
// Auth: `opencode auth`.
func NewOpenCodeDriver() *GenericLineDriver {
	return &GenericLineDriver{
		TypeID: "opencode",
		Models: []string{"gpt-4o", "claude-sonnet-4-5", "qwen-coder"},
		Binary: "opencode",
		ArgsFn: func(opts RunOpts) []string {
			a := []string{"run"}
			if opts.Model != "" {
				a = append(a, "--model", opts.Model)
			}
			a = append(a, opts.Prompt)
			return a
		},
	}
}

// NewAmpDriver returns a driver for the Sourcegraph Amp CLI.
// Auth: `amp login`.
func NewAmpDriver() *GenericLineDriver {
	return &GenericLineDriver{
		TypeID: "amp",
		Models: []string{"claude-sonnet-4-5", "gpt-5"},
		Binary: "amp",
		ArgsFn: func(opts RunOpts) []string {
			a := []string{"run", "--quiet"}
			if opts.Model != "" {
				a = append(a, "--model", opts.Model)
			}
			a = append(a, opts.Prompt)
			return a
		},
	}
}

// NewQwenDriver returns a driver for the Qwen Code CLI.
// Auth: `qwen auth`.
func NewQwenDriver() *GenericLineDriver {
	return &GenericLineDriver{
		TypeID: "qwen",
		Models: []string{"qwen3-coder", "qwen2.5-coder"},
		Binary: "qwen",
		ArgsFn: func(opts RunOpts) []string {
			a := []string{"run"}
			if opts.Model != "" {
				a = append(a, "--model", opts.Model)
			}
			a = append(a, opts.Prompt)
			return a
		},
	}
}

// NewDroidDriver returns a driver for the Factory Droid CLI.
// Auth: `droid auth` (Factory account required).
func NewDroidDriver() *GenericLineDriver {
	return &GenericLineDriver{
		TypeID: "droid",
		Models: []string{"factory-pro", "factory-fast"},
		Binary: "droid",
		ArgsFn: func(opts RunOpts) []string {
			a := []string{"task"}
			if opts.Model != "" {
				a = append(a, "--model", opts.Model)
			}
			a = append(a, opts.Prompt)
			return a
		},
	}
}

// NewCCRDriver returns a driver for the Claude Code Router CLI. CCR reuses
// the user's existing `claude` auth and routes through alternative models.
// Auth: same as Claude Code (`claude /login`).
func NewCCRDriver() *GenericLineDriver {
	return &GenericLineDriver{
		TypeID: "ccr",
		Models: []string{"claude-sonnet-4-5", "claude-opus-4-7", "claude-haiku-4-5"},
		Binary: "ccr",
		ArgsFn: func(opts RunOpts) []string {
			// `ccr run --model <m> --prompt <p>` per CCR's CLI surface.
			a := []string{"run"}
			if opts.Model != "" {
				a = append(a, "--model", opts.Model)
			}
			a = append(a, "--prompt", opts.Prompt)
			return a
		},
	}
}
