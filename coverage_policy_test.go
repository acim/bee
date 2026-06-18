package bee

import (
	"os"
	"strings"
	"testing"
)

func TestMakefileTestCovEnforcesCoverageThreshold(t *testing.T) {
	makefileBytes, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}

	makefile := string(makefileBytes)

	if !strings.Contains(makefile, "COVERAGE_THRESHOLD ?= 90.0") {
		t.Fatal("Makefile must define the 90% coverage threshold")
	}

	if !strings.Contains(makefile, `coverage >= threshold`) {
		t.Fatal("make test-cov must fail when total coverage is below the threshold")
	}

	if count := strings.Count(makefile, "go tool cover -func coverage.out"); count != 1 {
		t.Fatalf("make test-cov must only generate the coverage report once, found %d", count)
	}
}
