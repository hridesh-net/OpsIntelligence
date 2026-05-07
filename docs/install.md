# Install

Go **1.26+** is required when building from source ([`go.mod`](https://github.com/hridesh-net/OpsIntelligence/blob/main/go.mod)).

## Release installer (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/hridesh-net/OpsIntelligence/main/install.sh | bash
```

### Pin a version

```bash
OPSINTELLIGENCE_VERSION=v0.3.50 bash install.sh
```

Adjust the tag to match [GitHub Releases](https://github.com/hridesh-net/OpsIntelligence/releases).

## Build from source

```bash
git clone https://github.com/hridesh-net/OpsIntelligence.git
cd OpsIntelligence
FORCE_BUILD=1 bash install.sh
```

From a checkout you can also use:

```bash
make build    # → ./bin/opsintelligence
```

The installer places `opsintelligence` in `/usr/local/bin` or `~/.local/bin`, scaffolds `~/.opsintelligence/`, and can register a login service so the gateway starts after sign-in. Use `SKIP_SERVICE=1` to skip service registration.

## Locked-down or offline installs

| Scenario | Guidance |
| -------- | -------- |
| No surprise downloads | Set `NO_SOURCE_FALLBACK=1` and `OPSINTELLIGENCE_SKIP_GO_BOOTSTRAP=1` so install succeeds only when the binary or a local Go toolchain is already usable. |
| Tagged artifact only | Download from [Releases](https://github.com/hridesh-net/OpsIntelligence/releases); pin with `OPSINTELLIGENCE_VERSION`. |
| Airgap | Copy `opsintelligence` and optional `skills/` from a connected machine, `chmod +x`, set `STATE_DIR` — the shell installer is optional. |

## Environment toggles

| Variable | Default | Purpose |
| -------- | ------- | ------- |
| `OPSINTELLIGENCE_VERSION` | `latest` | Release tag to install |
| `INSTALL_DIR` | `/usr/local/bin` | Binary install path |
| `STATE_DIR` | `~/.opsintelligence` | Config and datastore root |
| `FORCE_BUILD=1` | — | Build from source even when a release binary exists |
| `NO_SOURCE_FALLBACK=1` | — | Do not fall back to source build when release asset is missing |
| `OPSINTELLIGENCE_SKIP_GO_BOOTSTRAP=1` | — | Do not download Go from go.dev when building from source |
| `OPSINTELLIGENCE_BOOTSTRAP_GO_VERSION` | `1.26.2` | Bootstrap Go version (must satisfy `go.mod`) |
| `SKIP_VENV=1` | — | Skip Python venv for the tool sandbox |
| `SKIP_SERVICE=1` | — | Skip launchd/systemd registration |
| `WITH_MEMPALACE=1` | — | Bootstrap managed MemPalace after install |
| `WITH_GEMMA=1` | — | Download the default Gemma GGUF for local-intel |

### Release binaries and Gemma

If GitHub returns `404` for an asset, the installer may **fall back to a source build** unless `NO_SOURCE_FALLBACK=1`. Without Go on `PATH`, it can bootstrap Go from go.dev once unless `OPSINTELLIGENCE_SKIP_GO_BOOTSTRAP=1`.

GitHub caps release assets at **2 GiB**; default Gemma GGUF may ship as a mirror manifest. Use onboarding / `local-intel setup`, **`OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_URL`**, or **`--url`** as documented in the README.

**Linux arm64** release builds may use **`fts5` only** (no in-process Gemma on musl cross-builds). Prefer cloud LLMs, or build on-device with glibc and **`EXTRA_TAGS=opsintelligence_localgemma`** when embedded Gemma is required.

## Uninstall

```bash
bash uninstall.sh                         # binary + service; keep state
bash uninstall.sh --purge                 # remove everything including ops.db
bash uninstall.sh --purge --keep-datastore # wipe state but preserve users/RBAC
```

`--keep-datastore` helps when moving hosts: users, roles, API keys, and audit data survive for the next install.

## Next steps

1. `./bin/opsintelligence onboard` — writes `opsintelligence.yaml` under the state dir  
2. `./bin/opsintelligence init` — seed example team templates  
3. `./bin/opsintelligence doctor` — validate config and reachability  
4. `./bin/opsintelligence start` — run the daemon  

See [Configuration](configuration.md) and the commented example [`.opsintelligence.yaml.example`](https://github.com/hridesh-net/OpsIntelligence/blob/main/.opsintelligence.yaml.example).
