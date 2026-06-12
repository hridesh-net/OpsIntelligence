# Quickstart

Get OpsIntelligence running and execute your first agent task in under 5 minutes.

## 1. Install

=== "macOS (Homebrew)"

    ```bash
    brew install opsintelligence/tap/opsintelligence
    ```

=== "Linux (curl)"

    ```bash
    curl -fsSL https://opsintelligence.dev/install.sh | bash
    ```

=== "Build from source"

    ```bash
    git clone https://github.com/opsintelligence/opsintelligence.git
    cd opsintelligence
    go build -o opsintelligence ./cmd/opsintelligence
    ./opsintelligence --version
    ```

## 2. Configure a provider

Create the config file at `~/.opsintelligence/opsintelligence.yaml`:

```yaml title="~/.opsintelligence/opsintelligence.yaml"
providers:
  openai:
    api_key: "sk-your-key-here"
    default_model: "gpt-4o-mini"
```

Or use an environment variable:

```yaml
providers:
  openai:
    api_key: "${OPENAI_API_KEY}"
    default_model: "gpt-4o-mini"
```

## 3. First run

```bash
opsintelligence run "Hello, what can you do?"
```

On first run, you'll be prompted to create an **owner account**. This is the admin user with full permissions.

```
Username: admin
Password: ************
Confirm:  ************
```

## 4. Start the server

```bash
opsintelligence serve
```

The server starts on `http://localhost:8080`.

- Dashboard: `http://localhost:8080/dashboard/app`
- API: `http://localhost:8080/api/v1`

## 5. Add a repo (optional)

```bash
opsintelligence repos add myorg/myrepo --platform github
```

This indexes the repository for code search and call graph generation.

## 6. Try the dashboard

Open `http://localhost:8080/dashboard/app` and sign in with the owner credentials you created.

Key pages to explore:

- **Overview** — system status and quick links
- **Run trace** — live agent execution log
- **Settings** → **LLM providers** — add more models
- **Settings** → **DevOps** — connect GitHub, Jenkins, SonarQube

## Next steps

- [User Guide](../guides/user-guide/index.md) — daily operations and workflows
- [Configuration](../configuration.md) — full config reference
- [Multi-Agent Systems in Go](../guides/multi-agent-go/index.md) — learn the concepts behind the engine
