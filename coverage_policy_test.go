package bee

import (
	"os"
	"strings"
	"testing"
)

func TestMakefileTestEnforcesCoverageThreshold(t *testing.T) {
	makefileBytes, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}

	makefile := string(makefileBytes)

	if !strings.Contains(makefile, ".PHONY: lint test update") {
		t.Fatal("Makefile must only define the test action for running tests")
	}

	if !strings.Contains(makefile, "COVERAGE_THRESHOLD ?= 95.0") {
		t.Fatal("Makefile must define the 95% coverage threshold")
	}

	if !strings.Contains(makefile, "go test -race -coverprofile=coverage.out ./...") {
		t.Fatal("make test must run the race detector and collect coverage")
	}

	if !strings.Contains(makefile, "README coverage badge $$badge% does not match measured coverage $$coverage%") {
		t.Fatal("make test must fail when the README coverage badge is stale")
	}

	if !strings.Contains(makefile, `$$coverage >= $$threshold`) {
		t.Fatal("make test must fail when total coverage is below the threshold")
	}

	if count := strings.Count(makefile, "go tool cover -func coverage.out"); count != 1 {
		t.Fatalf("make test must only generate the coverage report once, found %d", count)
	}
}
