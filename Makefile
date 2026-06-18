COVERAGE_THRESHOLD ?= 90.0

.PHONY: lint test testv test-cov update

lint:
	@golangci-lint run

test:
	@go test ./...

testv:
	@go test -v ./...

test-cov:
	@go test -coverprofile=coverage.out ./...
	@coverage_report="$$(go tool cover -func coverage.out)"; \
		echo "$${coverage_report}"; \
		coverage="$$(printf '%s\n' "$${coverage_report}" | awk '/^total:/ { sub(/%/, "", $$3); print $$3 }')"; \
		echo "Coverage: $${coverage}%"; \
		awk -v coverage="$${coverage}" -v threshold="$(COVERAGE_THRESHOLD)" 'BEGIN { exit coverage >= threshold ? 0 : 1 }'

update:
	@go get -u
