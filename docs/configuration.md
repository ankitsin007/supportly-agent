# Configuration

The agent reads YAML from `--config` (or `/etc/supportly/agent.yaml` if not provided). All values can be overridden by environment variables.

## Minimum

```yaml
project_id: 41e3a85a-9825-4a93-83bf-7ebee043b1e1
api_key: sk_live_...
sources:
  - type: docker
    enabled: true
```

That's enough for a Docker host. The agent will tail every container's stdout/stderr and ship matched exceptions to `https://ingest.supportly.io`.

## Full reference

```yaml
# --- Identity ---
project_id: <uuid>                                  # required
api_key: <key>                                       # required (or via SUPPORTLY_API_KEY)
api_endpoint: https://ingest.supportly.io           # default

# --- Log sources (all share the same parser pipeline) ---
sources:
  - type: file
    enabled: true
    paths:
      - /var/log/myapp/*.log

  - type: docker
    enabled: true
    socket: /var/run/docker.sock                     # default
    exclude_containers:
      - healthcheck
      - prometheus

  - type: journald
    enabled: true
    units:                                           # empty = all units
      - nginx.service
      - myapp.service

  - type: kubernetes
    enabled: true
    pod_log_root: /var/log/pods                      # default
    exclude_namespaces:
      - kube-system
      - kube-public
      - kube-node-lease

# --- TLS (only relevant for self-hosted Supportly with internal CAs) ---
tls:
  ca_bundle: /etc/supportly/ca.pem                   # adds to system roots, doesn't replace
  cert_pin: sha256/abc...                            # SPKI hash; survives cert rotation
  client_cert: /etc/supportly/agent.crt              # mTLS client cert
  client_key: /etc/supportly/agent.key
  skip_verify: false                                 # NEVER set in prod
  i_understand_this_is_insecure: false               # gate flag for skip_verify

# --- Rate limiting (token bucket) ---
rate_limits:
  per_source_eps: 100                                # sustained events/sec
  burst: 500                                         # burst capacity

# --- PII redaction (applied before envelopes leave the host) ---
redaction:
  enabled: true
  patterns:                                          # empty = all builtins
    - email
    - ipv4
    - ipv6
    - jwt
    - bearer
    - stripe_key
    - generic_api_key
  custom:                                            # arbitrary regexes
    - 'ssn=\d{3}-\d{2}-\d{4}'

# --- On-disk envelope buffer (network-outage survival) ---
buffer:
  enabled: true
  path: /var/lib/supportly/agent/queue
  max_disk_mb: 500                                   # oldest entries evicted past cap
  replay_interval_seconds: 30                        # how often to retry buffered entries
```

## Environment variables (override YAML)

| Env var | YAML equivalent |
|---|---|
| `SUPPORTLY_PROJECT_ID` | `project_id` |
| `SUPPORTLY_API_KEY` | `api_key` |
| `SUPPORTLY_API_ENDPOINT` | `api_endpoint` |

Useful for Docker / Helm where YAML config files are awkward to ship.

## Reload

The agent reads its config once at startup. To pick up new config, restart it:

```bash
docker restart supportly-agent
# OR
sudo systemctl restart supportly-agent
# OR
kubectl -n supportly rollout restart daemonset/supportly-agent
```
