package cli

import (
	"bytes"
	"testing"

	"github.com/stianfro/kvdrain/internal/coordinator"
)

func TestCommandValidation(t *testing.T) {
	tests := [][]string{{"drain"}, {"status", "one", "two"}, {"completion", "unknown"}, {"--output", "json", "version"}, {"--json", "-o", "wide", "version"}}
	for _, args := range tests {
		var out bytes.Buffer
		cmd := NewRootCommand(&out, &out)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("args %v unexpectedly succeeded", args)
		}
		if code := coordinator.ExitCode(err); code != 2 {
			t.Fatalf("args %v exit code %d, err %v", args, code, err)
		}
	}
}
func TestVersionDoesNotNeedCluster(t *testing.T) {
	var out bytes.Buffer
	cmd := NewRootCommand(&out, &out)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}
