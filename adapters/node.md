# Node.js — OTel auto-instrument adapter

## Install

```bash
npm install @opentelemetry/api-logs @opentelemetry/sdk-logs \
            @opentelemetry/exporter-logs-otlp-http \
            @opentelemetry/resources @opentelemetry/semantic-conventions
```

## Wire-in (10 lines)

Create `instrumentation.js` next to your entry point:

```javascript
const { logs } = require('@opentelemetry/api-logs');
const { LoggerProvider, BatchLogRecordProcessor } = require('@opentelemetry/sdk-logs');
const { OTLPLogExporter } = require('@opentelemetry/exporter-logs-otlp-http');
const { Resource } = require('@opentelemetry/resources');
const { SemanticResourceAttributes: SRA } = require('@opentelemetry/semantic-conventions');

const provider = new LoggerProvider({
  resource: new Resource({
    [SRA.SERVICE_NAME]: 'my-service',
    [SRA.DEPLOYMENT_ENVIRONMENT]: 'production',
  }),
});
provider.addLogRecordProcessor(new BatchLogRecordProcessor(
  new OTLPLogExporter({ url: 'http://127.0.0.1:4318/v1/logs' }),
));
logs.setGlobalLoggerProvider(provider);

module.exports = logs.getLogger('app');
```

Require it BEFORE any other imports in your entry point:

```javascript
const log = require('./instrumentation');
// ...rest of your app
```

## Use

```javascript
const log = require('./instrumentation');

try {
  await processOrder(orderId);
} catch (err) {
  log.emit({
    severityNumber: 17,  // ERROR
    severityText: 'ERROR',
    body: `${err.message}\n${err.stack}`,
    attributes: { 'order.id': orderId },
  });
}
```

## Express middleware (catch every uncaught error)

```javascript
const log = require('./instrumentation');

app.use((err, req, res, next) => {
  log.emit({
    severityNumber: 17,
    severityText: 'ERROR',
    body: `${err.message}\n${err.stack}`,
    attributes: {
      'http.method': req.method,
      'http.url': req.originalUrl,
    },
  });
  next(err);
});
```

## Production overrides

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=https://otel.your-network.example/v1/logs
```
The exporter respects this env var when no `url` is passed.
