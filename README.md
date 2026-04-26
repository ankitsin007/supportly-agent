# Supportly Agent

Drop-in error capture for [Supportly](https://supportly.io). Tails your log files and ships exceptions to your Supportly dashboard — **no SDK changes required in your app.**

> **Status:** pre-release (Week 1 of M1). Not yet production-ready. See the [M1 design doc](https://github.com/ankitsin007/Supportly/blob/main/docs/AGENT_M1_DESIGN.md) for the full plan.

---

## Why?

Supportly's SDKs (Python, Node, etc.) give the highest-fidelity error capture, but they require code changes in your app. The Supportly Agent is the alternative: install one binary on your server, point it at your existing log files, and start seeing exceptions in your dashboard within minutes — without touching application code.

Both work side-by-side. SDK gives you full stack traces and request context. Agent catches anything that escapes the SDK or anything in apps you can't easily modify.

## Quick start (development)

You'll need a running Supportly instance and a project's API key.

```bash
# 1. Build
git clone https://github.com/ankitsin007/supportly-agent
cd supportly-agent
make build

# 2. Configure
export SUPPORTLY_PROJECT_ID=<your-project-uuid>
export SUPPORTLY_API_KEY=<your-api-key>
export SUPPORTLY_API_ENDPOINT=http://localhost:8002/api/v1/ingest/events

# 3. Edit examples/demo.yaml to point at a log file you control, then:
./bin/supportly-agent --config examples/demo.yaml --log-level=debug

# 4. Smoke test (in another terminal):
make demo
```

Within ~5 seconds you should see a new issue in your Supportly dashboard.

## Production install (target — not yet shipped)

The full M1 will support all the install paths below. Today, only `make build` works.

```bash
# Docker (auditable form)
docker run -d --restart=always --name supportly-agent \
  -e SUPPORTLY_API_KEY=sk_... \
  -e SUPPORTLY_PROJECT_ID=... \
  -v /var/log:/host/var/log:ro \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  ghcr.io/ankitsin007/supportly-agent:latest

# One-liner (marketing form)
curl -fsSL https://get.supportly.io | sh -s -- --api-key sk_... --project-id ...
```

## Architecture

```
log files / docker / journald / k8s pods
                ↓
            Source(s)
                ↓
         RawLog channel
                ↓
       Parser pipeline (layered)
       1. JSON detector
       2. Framework regex bank   (Week 2)
       3. Heuristic              (Week 2)
       4. Fallback (always matches)
                ↓
      EventEnvelope (matches Supportly's existing ingest schema)
                ↓
            HTTP sink
                ↓
   POST /api/v1/ingest/events
```

Identical envelope shape to the SDKs — Supportly can't tell the difference.

## What's in Week 1

- [x] Go module skeleton + CI
- [x] `Source` interface (designed for M5 eBPF/OTel future)
- [x] `FileSource` (inotify tail, rotation handling)
- [x] Parser pipeline + JSON layer + Fallback layer
- [x] HTTP sink with retry/backoff
- [x] Config loader (YAML + env var overrides)
- [x] Unit tests for parser, sink, config
- [x] `make demo` end-to-end smoke

## What's coming

| Week | Adds |
|---|---|
| 2 | `DockerSource`, regex banks for Python/Java/Go/Node/Ruby tracebacks, on-disk ring buffer, rate limiter |
| 3 | `JournaldSource`, `KubernetesSource`, full TLS tier (custom CA, mTLS, cert pin), security review |
| 4 | Supportly UI: agent enrollment + agents list page |
| 5 | `get.supportly.io` install script, signed binaries, closed beta |
| 6 | GA: docs site, Helm chart, status page |

## Configuration

The agent reads YAML from `--config` (or `/etc/supportly/agent.yaml` by default). Environment variables override:

| Env var | YAML key | Required | Default |
|---|---|---|---|
| `SUPPORTLY_PROJECT_ID` | `project_id` | yes | — |
| `SUPPORTLY_API_KEY` | `api_key` | yes | — |
| `SUPPORTLY_API_ENDPOINT` | `api_endpoint` | no | `https://ingest.supportly.io` |

See [`examples/demo.yaml`](examples/demo.yaml) for the source configuration shape.

## Development

```bash
make test       # go test -race ./...
make lint       # golangci-lint run ./...
make fmt        # gofmt -w .
make build      # CGO_ENABLED=0 go build → bin/supportly-agent
```

## License

MIT — see [LICENSE](LICENSE).

## Security

Outbound HTTPS only. No inbound listeners. PII redaction (Week 2). Full security model in the [M1 design doc §10](https://github.com/ankitsin007/Supportly/blob/main/docs/AGENT_M1_DESIGN.md).

To report a vulnerability: open a GitHub Security Advisory or email security@supportly.io.
