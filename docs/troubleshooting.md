# Troubleshooting

## Agent doesn't appear in the dashboard

**1. Check `/healthz`:**
```bash
curl -s http://127.0.0.1:9876/healthz | jq .
```
- Status `ok` and at least one source healthy → agent is alive; the issue is on the network/auth side.
- No response at all → process isn't running. See logs:
  - Docker: `docker logs supportly-agent`
  - systemd: `journalctl -u supportly-agent -e`
  - k8s: `kubectl -n supportly logs -l app.kubernetes.io/name=supportly-agent`

**2. Verify network reachability:**
```bash
curl -fsS https://ingest.supportly.io/health
```
- Connection refused / timeout → firewall is blocking outbound HTTPS to ingest.supportly.io.
- 200 → network is fine; check API key.

**3. Verify credentials:**
```bash
# From inside the agent host, test ingest auth:
curl -X POST https://ingest.supportly.io/api/v1/ingest-agents/heartbeat \
  -H "X-Agent-Token: <bearer>" \
  -d '{"events_shipped":0,"events_dropped":0}' \
  -H "Content-Type: application/json"
```
401 → token revoked or wrong; re-enroll from the dashboard.

## No events arrive after triggering an exception

**1. Did the parser match?** Set `--log-level=debug` and look for `envelope shipped` lines:
```
time=... level=DEBUG msg="envelope shipped" event_id=... parser=python
```
- Logged → shipped to Supportly; the issue is dashboard-side.
- Not logged → no parser matched. Either the format isn't recognized, or the source isn't picking up the file.

**2. Is the source seeing data?** `/healthz` shows per-source `lines_emitted`. If it's 0, the source isn't reading anything:
- File source: confirm the path is mounted into the agent's filesystem.
- Docker source: confirm `/var/run/docker.sock` is mounted with `:ro` and the agent has docker group membership (or runs as root inside the container).
- journald: confirm `journalctl` is on PATH inside the container.

**3. Was it rate-limited?** /healthz shows `events_dropped`. A burst above the configured `rate_limits.burst` (default 500) is sampled.

## "TLS handshake error" against self-hosted Supportly

The agent's default trust set is the OS root pool + Mozilla bundle. Internal-CA-signed certs need either:

```yaml
tls:
  ca_bundle: /etc/supportly/internal-ca.pem
```

Or, for cert pinning:

```yaml
tls:
  cert_pin: sha256/<base64-spki-hash>
```

NEVER set `skip_verify: true` in production. The agent emits a heartbeat metric `agent.tls_skip_verify=true` and the dashboard will show a red banner if you do.

## Buffer disk usage growing

The on-disk buffer at `/var/lib/supportly/agent/queue/` indicates either:
- Network is down — agent buffers + retries every `buffer.replay_interval_seconds` (default 30).
- Supportly is returning 4xx — agent does NOT buffer 4xx (those are permanent failures).

To inspect:
```bash
ls -la /var/lib/supportly/agent/queue/ | head
```
Each `.json` file is one envelope. They're FIFO-named by sequence number.

When the buffer hits `max_disk_mb` (default 500), oldest entries are evicted. The dashboard's agent detail page shows current buffer size.

## High CPU usage

Most often: a single misbehaving service producing a high-volume error stream. Look at `/healthz` to see which source is emitting most:

```json
"sources": [
  {"name": "docker:bad-service", "lines_emitted": 1234567, ...},
  {"name": "docker:web", "lines_emitted": 42, ...}
]
```

Either fix the source app, or exclude that container:
```yaml
sources:
  - type: docker
    exclude_containers: [bad-service]
```

## Container restarts in a loop (Kubernetes)

Common causes:
- **Secret missing** — DaemonSet pods fail with `secrets "supportly-agent" not found`. Run `kubectl create secret` per the install doc.
- **`runAsNonRoot` violation** — only happens if you've overridden the image with one that doesn't have a non-root user. The official image runs as `nonroot:nonroot` (UID 65532).

## Reporting a bug

Include:
1. Output of `curl -s 127.0.0.1:9876/healthz`
2. Last 50 lines of agent logs
3. Output of `supportly-agent --version` (or container image tag)
4. Topology (Docker / systemd / k8s)

Open at https://github.com/ankitsin007/supportly-agent/issues.
