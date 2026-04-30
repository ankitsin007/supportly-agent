# Java — OTel auto-instrument adapter

## Zero-code path (recommended for Spring Boot / Dropwizard)

Download the OTel Java agent and run your app with the `-javaagent` flag.
No code change needed.

```bash
curl -L -O https://github.com/open-telemetry/opentelemetry-java-instrumentation/releases/latest/download/opentelemetry-javaagent.jar
```

Then start your app with these env vars + the agent:

```bash
export OTEL_SERVICE_NAME=my-service
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment=production
export OTEL_LOGS_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://127.0.0.1:4318/v1/logs
export OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/protobuf

# Optional — disable traces and metrics if you only want logs
export OTEL_TRACES_EXPORTER=none
export OTEL_METRICS_EXPORTER=none

java -javaagent:opentelemetry-javaagent.jar -jar your-app.jar
```

The agent auto-attaches to Logback / Log4j / java.util.logging and ships
log records to the local Supportly agent. No imports, no code edits.

## Programmatic path (plain Java + Maven, no Spring)

If you can't use the javaagent (e.g., locked-down classpath):

### `pom.xml`

```xml
<dependency>
    <groupId>io.opentelemetry</groupId>
    <artifactId>opentelemetry-sdk</artifactId>
    <version>1.42.0</version>
</dependency>
<dependency>
    <groupId>io.opentelemetry</groupId>
    <artifactId>opentelemetry-exporter-otlp</artifactId>
    <version>1.42.0</version>
</dependency>
```

### Initialization

```java
import io.opentelemetry.api.logs.Severity;
import io.opentelemetry.exporter.otlp.http.logs.OtlpHttpLogRecordExporter;
import io.opentelemetry.sdk.logs.SdkLoggerProvider;
import io.opentelemetry.sdk.logs.export.BatchLogRecordProcessor;
import io.opentelemetry.sdk.resources.Resource;
import io.opentelemetry.semconv.ResourceAttributes;

public class OtelInit {
    public static SdkLoggerProvider provider() {
        return SdkLoggerProvider.builder()
            .setResource(Resource.create(Attributes.builder()
                .put(ResourceAttributes.SERVICE_NAME, "my-service")
                .put(ResourceAttributes.DEPLOYMENT_ENVIRONMENT, "production")
                .build()))
            .addLogRecordProcessor(BatchLogRecordProcessor.builder(
                OtlpHttpLogRecordExporter.builder()
                    .setEndpoint("http://127.0.0.1:4318/v1/logs")
                    .build()
            ).build())
            .build();
    }
}
```

Call `OtelInit.provider()` once at startup and bridge it into your
logging framework via the OTel Logback / Log4j appender packages.

## Verify

After your app starts, hit `http://localhost:9876/healthz` on the agent
host. The `otel:127.0.0.1:4318` source should show `lines_emitted >= 1`
once the first log fires.
