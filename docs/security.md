# Security review pack

For SecOps teams reviewing the Supportly Agent before installing it on production hosts.

## Network

- **Outbound HTTPS only.** No inbound listeners other than `127.0.0.1:9876/healthz` (loopback, no auth needed).
- **Pinned domain.** Connects only to the configured `api_endpoint` (default `https://ingest.supportly.io`).
- **Optional egress proxy.** Respects `HTTP_PROXY` / `HTTPS_PROXY`.
- **TLS 1.2 minimum.** Configurable to require an internal CA, mTLS, or SPKI pin (see [TLS section](#tls-tier)).

## Auth

- **Per-host identity.** Each agent presents a unique enrollment token at first connect; that token is single-use and exchanged for a long-lived agent bearer. Bearer is stored sha256-hashed server-side; the plaintext exists only in the agent's local config.
- **Revocation.** Revoking from the dashboard immediately stops the bearer from authenticating; existing buffered events from that host are not retroactively expunged.

## What the agent CAN read

| Resource | How it's accessed |
|---|---|
| Log files explicitly mounted | Bind mounts (you control which paths) |
| Container/pod labels | `docker info`, kubelet `/var/log/pods/...` directory names |
| Hostname / kernel / OS | `uname` |
| Docker socket | Read-only API methods only (`docker logs`, `docker events`) |

## What the agent CANNOT read

- Your application source code.
- Container env vars (we don't bind-mount `/proc/<pid>/environ`).
- `/etc/shadow`, SSH keys, anything you didn't explicitly grant.
- Any host outside its ingest endpoint.

## PII redaction

Applied **before** envelopes leave the host. Built-in patterns:

| Pattern | Replaced with |
|---|---|
| Email addresses | `[email]` |
| IPv4 / IPv6 | `[ip]` |
| JWTs (eyJ-prefixed three-part tokens) | `[jwt]` |
| `Authorization: Bearer ...` | `Bearer [token]` |
| Stripe-style API keys (`sk_live_*`, `pk_test_*`) | `[api_key]` |
| Generic `api_key=...` patterns | `[api_key]` |

Custom regexes can be added via `redaction.custom`. See [Configuration](configuration.md).

## Supply chain

- **Reproducible builds.** Go + locked module checksums. SBOM (SPDX JSON) published per release.
- **Signed binaries.** Each release archive + `checksums.txt` is signed via Sigstore keyless OIDC. Verifiable with `cosign verify-blob` (see [install.md](install.md#verify-checksums-recommended)).
- **Signed container.** `ghcr.io/ankitsin007/supportly-agent` is signed via `cosign sign` keyless. Verifiable with `cosign verify`.
- **Distroless runtime.** Container image is `gcr.io/distroless/static-debian12:nonroot` — no shell, no package manager, runs as UID 65532.
- **Static binary.** `CGO_ENABLED=0` — no glibc dependency, no shared libraries.
- **Quarterly dependency review.** Dependabot enabled for CVE alerts.

## TLS tier (self-hosted Supportly only)

For customers running their own Supportly instance with internal-CA-signed certificates:

| Tier | Config | Use |
|---|---|---|
| **0. Public CA (default)** | nothing | SaaS Supportly |
| **1. Custom CA bundle** | `tls.ca_bundle: /path/ca.pem` | Internal CA |
| **2. SPKI pin** | `tls.cert_pin: sha256/...` | Banks, gov |
| **3. Insecure** | `tls.skip_verify: true` AND `tls.i_understand_this_is_insecure: true` | Local dev only |

Tier 3 is gated by two flags on purpose — preventing accidental enable from a Stack Overflow snippet.

## Resource footprint

| Metric | Idle | 100 EPS | 1k EPS |
|---|---|---|---|
| Memory | ~30 MB | ~50 MB | ~80 MB |
| CPU (1 core) | <0.1% | ~0.5% | ~3% |
| Disk (buffer) | 0 | 0 | up to `max_disk_mb` (default 500 MB) |
| Network | 0 | ~36 GB/month outbound | ~360 GB/month outbound |

## Audit hooks

- The `tags.source: "agent"` field on every envelope marks it as agent-shipped (vs SDK).
- `tags.agent_version` lets you detect older versions still running.
- Heartbeat metrics include `agent.server_cert_expires_soon` (30 days before expiry) so misconfigurations surface early.

## Disclosure

To report a vulnerability:
- **Preferred:** open a [GitHub Security Advisory](https://github.com/ankitsin007/supportly-agent/security/advisories/new) (private).
- **Alternative:** email security@supportly.io with subject "agent vuln".

We respond within 48 hours and target a fix release within 14 days for criticals.
