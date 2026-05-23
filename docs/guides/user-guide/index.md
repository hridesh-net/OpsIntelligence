# User Guide

This guide covers daily operations: using the dashboard, the CLI, configuring providers, and running common workflows.

## Sections

| Chapter | What you'll learn |
|---------|-----------------|
| [Dashboard](dashboard.md) | Overview cards, run trace, tasks, users, settings |
| [CLI Reference](cli.md) | Every command with examples |
| [Providers](providers.md) | OpenAI, Anthropic, Gemini/Vertex, Ollama, OpenRouter |
| [Workflows](workflows.md) | PR review, incident response, autonomous maintenance |
| [Performance](performance.md) | Tuning memory, concurrency, cost reduction |
| [Troubleshooting](troubleshooting.md) | Common issues and fixes |

## Quick commands

```bash
# Health check
opsintelligence doctor
opsintelligence providers health

# One-shot task
opsintelligence run "Review PR #123"

# Async task with tail
opsintelligence run-async "Deploy staging"
opsintelligence tasks tail <id>

# List and sync repos
opsintelligence repos list
opsintelligence repos sync owner/repo
```
