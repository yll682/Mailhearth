package main

import (
	"os"
	"testing"
)

// The committed web/src/lib/api-types.ts must match the Go types; run
// `go run ./cmd/apitypes` from the repository root after changing them.
func TestCommittedTypesUpToDate(t *testing.T) {
	want, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../web/src/lib/api-types.ts")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatal("web/src/lib/api-types.ts is out of date: run `go run ./cmd/apitypes`")
	}
}
