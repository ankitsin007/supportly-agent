.PHONY: build test lint fmt clean demo run

BIN := supportly-agent
PKG := github.com/ankitsin007/supportly-agent
VERSION ?= 0.1.0
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/$(BIN) ./cmd/supportly-agent

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./... || echo "golangci-lint not installed: brew install golangci-lint"

fmt:
	gofmt -w .

clean:
	rm -rf bin/

# `make demo` — end-to-end smoke against local Supportly.
# Requires: Supportly running on http://localhost:8002, project credentials
# exported as SUPPORTLY_PROJECT_ID and SUPPORTLY_API_KEY.
demo: build
	@if [ -z "$$SUPPORTLY_PROJECT_ID" ] || [ -z "$$SUPPORTLY_API_KEY" ]; then \
		echo "ERROR: export SUPPORTLY_PROJECT_ID and SUPPORTLY_API_KEY first"; \
		exit 1; \
	fi
	@mkdir -p /tmp/supportly-agent-demo
	@echo "" > /tmp/supportly-agent-demo/app.log
	@SUPPORTLY_API_ENDPOINT=http://localhost:8002/api/v1/ingest/events \
	 ./bin/$(BIN) --config examples/demo.yaml --log-level=debug & \
	 echo $$! > /tmp/supportly-agent-demo/agent.pid
	@sleep 2
	@echo "Writing fake JSON error log line..."
	@echo '{"level":"error","message":"demo: payment processor timeout","exception":{"type":"TimeoutError","value":"connect timed out after 30s"},"timestamp":"$(shell date -u +%Y-%m-%dT%H:%M:%SZ)"}' >> /tmp/supportly-agent-demo/app.log
	@echo "Writing fake fallback (unstructured) error line..."
	@echo "2026-04-26 12:30:00 ERROR something blew up in the worker" >> /tmp/supportly-agent-demo/app.log
	@sleep 5
	@echo ""
	@echo "Demo complete. Check Supportly dashboard for two new events."
	@echo "To stop the agent: kill \$$(cat /tmp/supportly-agent-demo/agent.pid)"

run: build
	./bin/$(BIN) --config examples/demo.yaml --log-level=debug
