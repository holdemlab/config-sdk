.PHONY: all test lint vet build coverage coverage-html compat clean

all: lint vet test

## Run all tests with race detector
test:
	go test -count=1 -race ./...

## Run golangci-lint
lint:
	golangci-lint run ./...

## Run go vet
vet:
	go vet ./...

## Build all packages
build:
	go build ./...

## Run tests with coverage (≥90% gate)
coverage:
	@go test -count=1 -coverprofile=coverage.out $$(go list ./... | grep -v /examples/)
	@COVERAGE=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | tr -d '%'); \
	echo "Total coverage: $${COVERAGE}%"; \
	if [ "$$(echo "$$COVERAGE < 90" | bc -l)" -eq 1 ]; then \
		echo "FAIL: coverage $${COVERAGE}% is below 90%"; exit 1; \
	fi

## Open coverage report in browser
coverage-html: coverage
	go tool cover -html=coverage.out

## Run AES-GCM compatibility test
compat:
	go test -count=1 -run TestAESGCMServerCompatibility ./...

## Run integration tests against a real server (requires env vars)
integration:
	go test -tags=integration -count=1 -run TestLiveE2E ./...

## Remove generated files
clean:
	rm -f coverage.out
