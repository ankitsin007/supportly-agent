# Auto-instrument adapters

The agent's `otel` source (M5 Week 1) accepts standard OTLP/HTTP log
exports. These adapters give your apps the smallest possible
configuration to start exporting.

| Language / Runtime | File |
|---|---|
| Python (any framework) | [python.md](python.md) |
| Node.js (Express / Fastify / NestJS) | [node.md](node.md) |
| Java (Spring Boot / Dropwizard / plain) | [java.md](java.md) |

All adapters target `http://127.0.0.1:4318/v1/logs` by default — the
local agent's OTLP/HTTP listener. Override with `OTEL_EXPORTER_OTLP_ENDPOINT`
in production.

## CLI

`supportly-agent adapters` prints the snippet for a given language:

```bash
supportly-agent adapters python    # → snippet on stdout
supportly-agent adapters node      # → snippet on stdout
supportly-agent adapters java      # → snippet on stdout
supportly-agent adapters list      # → list available adapters
```

The same content is embedded in the binary so air-gapped customers
don't need network access to read it.
