# Changelog

All notable changes documented here. The format is loosely based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
follows [Semantic Versioning](https://semver.org/).

## [1.2.0] — 2026-05-01

M5 progress: deepest-fidelity tier (eBPF) scaffolding + OTel auto-
instrument adapters. Targets the "I want zero-code instrumentation"
customer who already runs OTel SDKs in production.

### Added
- **OTel/OTLP HTTP log receiver** (`type: otel` source). Listens on
  `127.0.0.1:4318` by default. Customers with existing OpenTelemetry
  log exporters can point them at the agent without any other changes.
  Wire types follow the OTLP/HTTP JSON spec; severity_number is banded
  to text when severity_text is absent. Resource attributes
  `service.name` / `service.namespace` / `service.version` / `host.name`
  are promoted to envelope tags.
- **`supportly-agent adapters <lang>` subcommand** prints per-language
  OTel auto-instrument snippets. Supports `python`, `node`, `java`.
  The snippets are embedded via `//go:embed` so air-gapped hosts can
  read them without docs/ being shipped alongside.
- **eBPF uprobe receiver scaffolding** (`type: ebpf` source). Linux-
  only — preflight checks kernel ≥ 5.4, CAP_BPF, `/sys/fs/bpf` mount.
  Per-language uprobe attach (Python `PyErr_Display`, JVM JVMTI, V8,
  Go `runtime.gopanic`, Ruby `rb_exc_raise`) is M5.1–M5.5 follow-up
  work; this release only ships the platform-portable scaffolding so
  cross-compile to macOS/Windows still works.

### Changed
- `SourceConfig` discriminator now accepts `otel` and `ebpf`.

### Notes
- `ebpf` source returns `ErrUnsupported` on macOS/Windows and is
  demoted to a startup warning rather than a fatal error — the rest
  of the agent (file/docker/journald/k8s/otel sources) keeps running.

## [1.1.0] — 2026-04-30

M5 Week 1: OTLP receiver landed (see 1.2.0 notes — bundled into the
release for clarity).

## [1.0.0] — 2026-04-26 (GA)

The first generally-available release. Everything from M1 — log capture
across four source types, multi-language traceback parsing, network-
outage survival, signed releases, Helm chart, full docs.

### Added
- **On-disk envelope buffer** for offline survival. FIFO, file-per-envelope,
  no external DB. Default cap 500 MB, evicts oldest entries past the cap.
  Replays on a configurable interval (default 30 s).
- **Helm chart** at `deploy/helm/`. DaemonSet pattern, RBAC, optional
  hostPath buffer for restart survival, optional `existingSecret` for
  SealedSecrets / external-secrets-operator users.
- **Documentation** at `docs/`:
  - `install.md` — every install path with copy-pasteable commands.
  - `configuration.md` — full YAML reference + env-var overrides.
  - `troubleshooting.md` — diagnose-from-zero playbook.
  - `security.md` — security review pack for SecOps teams.
- **Version bumped to 1.0.0.**

### Changed
- The replay loop logs `replayed buffered envelopes` only when at least
  one envelope was successfully shipped, keeping noise down during
  long outages.

### Notes
- M1 is now feature-complete per [docs/AGENT_M1_DESIGN.md](https://github.com/ankitsin007/Supportly/blob/main/docs/AGENT_M1_DESIGN.md).
- M2 (repo indexing for the diagnostic agent) starts next.

## [0.5.0] — 2026-04-26

### Added
- **install.sh** one-line installer (POSIX shell, ~250 lines, auto-detects
  Docker / systemd / kubernetes / binary topology).
- **GoReleaser pipeline** — cross-builds linux/darwin × amd64/arm64,
  SHA256 checksums, SPDX SBOMs (Syft), Cosign keyless signing.
- **Multi-arch container** at `ghcr.io/ankitsin007/supportly-agent`.
  Distroless runtime (~25 MB final image, no shell).
- **Release workflow** triggered on `git tag v*`.
- **Healthz endpoint** at `127.0.0.1:9876/healthz` — used by install.sh's
  post-install verify and by ops for local debugging.
- 10-test bash suite for `install.sh`.

## [0.3.0] — 2026-04-26

### Added
- **journald source** — talks to `journalctl -o json -f` so the agent
  stays CGO_ENABLED=0.
- **kubernetes source** — DaemonSet pattern, tails `/var/log/pods/*`,
  parses kubelet directory format for namespace + pod + deployment.
- **Tiered TLS** for self-hosted Supportly tenants:
  - Default: OS root pool + Mozilla bundle.
  - Custom CA bundle: `--ca-bundle path.pem` (additive, doesn't replace).
  - SPKI pin: `--cert-pin sha256/...` (survives cert rotation).
  - Insecure: requires both `--tls-skip-verify` AND
    `--i-understand-this-is-insecure` flags.
- **mTLS** via separate `--client-cert` / `--client-key` flags.

## [0.2.0] — 2026-04-26

### Added
- **Docker source** — direct Engine API access via `/var/run/docker.sock`,
  multiplex demuxer, watches `/events` for new containers.
- **5 framework parsers** — Python (Django/CPython tracebacks), Java
  (JVM Throwable.printStackTrace), Go (runtime panics), Node (V8
  error.stack), Ruby (modern + legacy traceback shapes).
- **Multi-line recombiner** — per-stream buffering with per-language
  continuation rules + a Universal continuation as fallback.
- **Token-bucket rate limiter** — default 100 EPS sustained, burst 500.
- **PII redactor** — emails, IPv4/v6, JWTs, Bearer tokens, Stripe-style
  keys, generic api_key= patterns. Customer-extensible via custom
  regex list.

## [0.1.0] — 2026-04-26

### Added
- Initial Week 1 scaffold:
  - `Source` interface (designed for M5 eBPF/OTel forward-compat).
  - `FileSource` (fsnotify tail with rotation handling).
  - Layered parser pipeline (JSON layer + Fallback keyword match).
  - HTTP sink with exponential backoff (5xx + network errors retry,
    4xx fail permanent).
  - YAML + env-var config loader.
  - Static API-key identity (cert exchange deferred to a follow-up).
  - CI: gofmt + vet + test + build, plus golangci-lint.
