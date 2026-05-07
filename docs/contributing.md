# Contributing

Thank you for helping improve OpsIntelligence. This page covers **docs and core dev workflow**; the repository also ships a fuller contributor guide at [`CONTRIBUTING.md`](https://github.com/hridesh-net/OpsIntelligence/blob/main/CONTRIBUTING.md) (adapter contracts, channel checklist, property tests).

## Prerequisites

- **Go**: version from [`go.mod`](https://github.com/hridesh-net/OpsIntelligence/blob/main/go.mod) (currently 1.26.x).  
- **CGO**: required for SQLite-related builds (`make build` uses `-tags fts5`).  
- **Python**: optional for some tool-factory / sandbox paths (see root `CONTRIBUTING.md`).

## Common commands

```bash
make build    # go build -tags fts5 ./cmd/opsintelligence → ./bin/opsintelligence
make test     # go test ./...
make lint     # gofmt + go vet
./bin/opsintelligence doctor --config .opsintelligence.yaml.example --skip-network
```

Integration clients are exercised under httptest in **`go test ./internal/devops/...`** (no live APIs required).

## Documentation

Preview the MkDocs site locally:

```bash
python3 -m venv .venv && .venv/bin/pip install -r requirements-docs.txt   # once, recommended on PEP 668 Python (Homebrew)
.venv/bin/mkdocs serve
```

Strict build (also used in CI):

```bash
.venv/bin/mkdocs build --strict
```

Open **`site/index.html`** after **`mkdocs build`** for a static preview.

### Architecture diagram export

The canonical runtime diagram lives at **`docs/architecture/diagrams/opsintelligence-architecture.drawio`**. After editing it in draw.io desktop, refresh the checked-in PNG (used by the README and MkDocs):

```bash
draw.io -x -f png -s 2 -b 10 \
  -o docs/architecture/diagrams/opsintelligence-architecture.png \
  docs/architecture/diagrams/opsintelligence-architecture.drawio
```

If `draw.io` is not on your `PATH` (common on macOS), invoke the app binary directly, for example:

```bash
/Applications/draw.io.app/Contents/MacOS/draw.io -x -f png -s 2 -b 10 \
  -o docs/architecture/diagrams/opsintelligence-architecture.png \
  docs/architecture/diagrams/opsintelligence-architecture.drawio
```

Installers: [draw.io desktop releases](https://github.com/jgraph/drawio-desktop/releases) or `brew install --cask drawio`.

## Pull requests

- Run **`make lint`** and **`make test`** before submitting.  
- Add **`CHANGELOG.md`** entries under `[Unreleased]` when user-visible behavior changes.  
- See [`CONTRIBUTING.md`](https://github.com/hridesh-net/OpsIntelligence/blob/main/CONTRIBUTING.md) for adapter-specific gates and contract tests.
