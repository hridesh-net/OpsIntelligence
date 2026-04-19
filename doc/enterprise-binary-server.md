# Enterprise binary server

This guide is for running **OpsIntelligence as a single long-lived binary** (not Kubernetes-first) under higher load: systemd supervision, reverse proxy + TLS, state layout, resource limits, and YAML knobs that tighten backpressure.

## Configuration highlights

Add or tune these keys in `opsintelligence.yaml`:

```yaml
gateway:
  host: "0.0.0.0"              # or your public hostname
  port: 18790
  bind: "lan"                  # match your exposure model
  max_websocket_clients: 500    # 0 = unlimited; caps live WS connections

agent:
  enterprise: true              # when planning is unset, enables planning by default
  planning: null                # omit or null to use enterprise default; explicit true/false wins
  subagent_tasks:
    max_concurrent: 16          # 0 = library default (8)
    retain_limit: 512         # 0 = library default (256)
    default_timeout: "45m"    # empty = library default (30m); Go duration syntax
```

- **`agent.enterprise`**: opts into stronger defaults for complex work. If `planning` is omitted (`nil`), planning turns **on**; if `planning` is explicitly set, that value always wins.
- **`gateway.max_websocket_clients`**: rejects new WebSocket handshakes when the cap is reached (HTTP API remains available). Use `0` for no cap.
- **`agent.subagent_tasks`**: bounds async sub-agent work (`subagent_run_async`, parallel runs, supervision). Zeros fall back to built-in defaults.

## systemd unit

Run the binary as a dedicated user with an explicit config and state directory:

```ini
[Unit]
Description=OpsIntelligence gateway
After=network-online.target

[Service]
Type=simple
User=opsintel
Group=opsintel
Environment=OPSINTELLIGENCE_STATE_DIR=/var/lib/opsintelligence
ExecStart=/usr/local/bin/opsintelligence start --config /etc/opsintelligence/opsintelligence.yaml
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

Adjust paths and flags to match your install (`start` vs foreground gateway-only commands).

## Reverse proxy and TLS

Terminate TLS at **nginx**, **Caddy**, or another edge proxy; forward to the gateway loopback:

- Proxy `https://your-host/` → `http://127.0.0.1:18790/` (or the port you configured).
- Preserve **WebSocket** upgrade headers (`Upgrade`, `Connection`) and reasonable **read/send timeouts** for long-lived agent streams.
- Set **`client_max_body_size`** (or equivalent) if you accept large uploads through the gateway.

Keep **`gateway.token`** / API keys / OIDC aligned with how the proxy authenticates (or terminates TLS only and passes through `Authorization`).

## State directory

Point **`state_dir`** (or `OPSINTELLIGENCE_STATE_DIR`) at a persistent volume:

- SQLite datastore, episodic memory, skills cache, run traces, and workspace files live under this tree.
- Back up this directory for disaster recovery; exclude ephemeral caches if you document them separately.

## Operating system limits

- Raise **open files** (`LimitNOFILE`) for many concurrent channels, MCP clients, and websocket clients.
- Monitor **memory** and **CPU**; sub-agents and planning add extra model calls—`agent.subagent_tasks` and `max_websocket_clients` are the primary in-process levers before horizontal scaling.

## Observability

- Use structured logs from the binary; ship with your log agent (e.g. journald → Promtail/Loki).
- Code entry points for metrics and tracing live under [`internal/observability/`](../internal/observability/) (correlation IDs, metrics, run-trace NDJSON). Wire your collector to whatever you enable in `opsintelligence.yaml` (e.g. OpenTelemetry).

## Optional multi-replica note

Multiple replicas of the same binary require shared understanding of **sessions**, **webhooks**, and **SQLite** (single-writer). For true horizontal scale, prefer externalizing the datastore and designing sticky sessions or a single gateway tier; that is outside the scope of this binary-focused document.
