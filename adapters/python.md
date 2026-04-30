# Python — OTel auto-instrument adapter

## Install

```bash
pip install opentelemetry-sdk opentelemetry-exporter-otlp-proto-http
```

## Wire-in (5 lines)

Add this to your app's startup (e.g. `main.py`, top of `app.py`, or
your Django `settings.py`):

```python
from opentelemetry._logs import set_logger_provider
from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
from opentelemetry.sdk.resources import Resource
import logging

provider = LoggerProvider(resource=Resource.create({
    "service.name": "my-service",
    "deployment.environment": "production",
}))
provider.add_log_record_processor(BatchLogRecordProcessor(
    OTLPLogExporter(endpoint="http://127.0.0.1:4318/v1/logs"),
))
set_logger_provider(provider)
logging.getLogger().addHandler(LoggingHandler(logger_provider=provider))
```

That's it. Every `logger.error(...)` / `logger.exception(...)` call now
ships to the agent and through to your Supportly dashboard.

## Verify

```python
import logging
logging.getLogger(__name__).error("supportly otel adapter test")
```

Then check `http://localhost:9876/healthz` on the agent host — the
`otel:127.0.0.1:4318` source should show `lines_emitted >= 1`.

## Production overrides

Set these env vars instead of hard-coding the endpoint:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=https://otel.your-network.example/v1/logs
export OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer%20...
```

## Caught exceptions vs unhandled

The handler above captures exceptions you log explicitly. To capture
ALL unhandled exceptions, add a `sys.excepthook`:

```python
import sys

def _ship_unhandled(exc_type, exc_value, tb):
    logging.getLogger("__main__").error(
        "unhandled exception", exc_info=(exc_type, exc_value, tb)
    )
    sys.__excepthook__(exc_type, exc_value, tb)

sys.excepthook = _ship_unhandled
```
