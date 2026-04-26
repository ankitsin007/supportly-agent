# Installing the Supportly Agent

The Supportly Agent ships exception events from your servers to your Supportly project, **without any SDK code in your application**. Pick the install path that fits your infrastructure.

| If you run on | Use |
|---|---|
| Docker hosts | [Docker](#docker) or the [one-liner](#one-liner) |
| Kubernetes | [Helm](#helm) (preferred) or [raw manifest](#raw-kubernetes-manifest) |
| systemd Linux | [systemd unit](#systemd) |
| Anything else | [Raw binary](#raw-binary) |

You'll need a Supportly **project ID** and either an **API key** or a **single-use enrollment token** from the dashboard at `https://app.supportly.io/dashboard/ingest-agents`.

---

## One-liner

Auto-detects your topology and installs accordingly. Reads the script first if you'd like — it's ~250 lines, hosted as a static file on GitHub.

```bash
curl -fsSL https://raw.githubusercontent.com/ankitsin007/supportly-agent/main/install.sh \
  | sh -s -- --api-key sk_... --project-id <uuid>
```

Flags:

| Flag | Purpose |
|---|---|
| `--api-key` | API key from the dashboard (or use `--enrollment-token`) |
| `--project-id` | Project UUID |
| `--enrollment-token` | Single-use token (preferred for prod — rotates at first connect) |
| `--version` | Pin to a release tag (default: latest) |
| `--endpoint` | Override the Supportly ingest URL (self-hosted Supportly users) |
| `--install-dir` | Where to drop the binary in systemd mode (default `/usr/local/bin`) |
| `--no-verify` | Skip the post-install health check |
| `--dry-run` | Print actions without running them |

---

## Docker

Single-host Docker deployments. Includes the docker socket so the agent can tail every container's stdout/stderr.

```bash
docker run -d --restart=always --name supportly-agent \
  -e SUPPORTLY_PROJECT_ID=<uuid> \
  -e SUPPORTLY_API_KEY=sk_... \
  -v /var/log:/host/var/log:ro \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v supportly-buffer:/var/lib/supportly/agent/queue \
  ghcr.io/ankitsin007/supportly-agent:latest
```

The named volume `supportly-buffer` lets the on-disk envelope queue survive container restarts.

---

## Helm

Kubernetes installs use Helm by default.

```bash
helm repo add supportly-agent \
  https://raw.githubusercontent.com/ankitsin007/supportly-agent/main/deploy/helm
helm repo update

helm install supportly supportly-agent/supportly-agent \
  --namespace supportly \
  --create-namespace \
  --set projectId=<uuid> \
  --set apiKey=sk_...
```

Production deployments using SealedSecrets / external-secrets:

```bash
# Skip the chart-managed Secret; point at one you've created yourself.
helm install supportly supportly-agent/supportly-agent \
  --namespace supportly \
  --set existingSecret=my-supportly-secret
```

The Secret must contain keys `project-id` and `api-key`.

See [`deploy/helm/values.yaml`](../deploy/helm/values.yaml) for all overridable values.

---

## Raw Kubernetes manifest

If you don't use Helm:

```bash
kubectl create namespace supportly
kubectl -n supportly create secret generic supportly-agent \
  --from-literal=project-id=<uuid> \
  --from-literal=api-key=sk_...
kubectl apply -f \
  https://raw.githubusercontent.com/ankitsin007/supportly-agent/main/deploy/k8s/daemonset.yaml
```

---

## systemd

For VMs and bare-metal Linux hosts running systemd. The one-liner above handles this automatically; the manual steps:

```bash
# 1. Download the binary for your arch.
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
VERSION=$(curl -fsSL https://api.github.com/repos/ankitsin007/supportly-agent/releases/latest \
  | grep tag_name | sed -E 's/.*"v?([^"]+)".*/\1/')
curl -fsSL "https://github.com/ankitsin007/supportly-agent/releases/download/v${VERSION}/supportly-agent_${VERSION}_linux_${ARCH}.tar.gz" \
  | tar -xz supportly-agent
sudo install -m 0755 supportly-agent /usr/local/bin/

# 2. Drop the env file.
sudo mkdir -p /etc/supportly
sudo tee /etc/supportly/agent.env >/dev/null <<EOF
SUPPORTLY_PROJECT_ID=<uuid>
SUPPORTLY_API_KEY=sk_...
EOF
sudo chmod 0600 /etc/supportly/agent.env

# 3. Drop the unit + start.
sudo tee /etc/systemd/system/supportly-agent.service >/dev/null <<'EOF'
[Unit]
Description=Supportly Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/supportly/agent.env
ExecStart=/usr/local/bin/supportly-agent
Restart=always
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
StateDirectory=supportly

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now supportly-agent
```

---

## Raw binary

For environments without Docker, systemd, or k8s (e.g., FreeBSD, weirder Linux). Download and run:

```bash
./supportly-agent --config /etc/supportly/agent.yaml
```

See [Configuration](configuration.md) for the YAML schema.

---

## Verify

The agent exposes `http://127.0.0.1:9876/healthz` for local sanity checks:

```bash
curl -s http://127.0.0.1:9876/healthz | jq .
```

Then check the **Shipping Agents** page in your dashboard. The agent should appear within 60s with status = `online`.

## Verify checksums (recommended for prod)

Every release is signed with [Sigstore](https://sigstore.dev) keyless OIDC. Verify before installing:

```bash
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp 'github.com/ankitsin007/supportly-agent' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
sha256sum -c checksums.txt
```

Container images are signed similarly:

```bash
cosign verify ghcr.io/ankitsin007/supportly-agent:latest \
  --certificate-identity-regexp 'github.com/ankitsin007/supportly-agent' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```
