package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra/doc"

	"github.com/stianfro/kvdrain/internal/cli"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: docgen <output-directory>")
		os.Exit(2)
	}
	base := os.Args[1]
	completionDir := filepath.Join(base, "completions")
	manDir := filepath.Join(base, "manpages")
	for _, path := range []string{completionDir, manDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			fatal(err)
		}
	}
	root := cli.NewRootCommand(io.Discard, io.Discard)
	if err := generateCompletions(root, completionDir); err != nil {
		fatal(err)
	}
	header := &doc.GenManHeader{Title: "KVDRAIN", Section: "1", Source: "kvdrain", Manual: "kvdrain Manual"}
	if err := doc.GenManTree(root, header, manDir); err != nil {
		fatal(err)
	}
}

func generateCompletions(root interface {
	GenBashCompletionFile(string) error
	GenZshCompletionFile(string) error
	GenFishCompletionFile(string, bool) error
	GenPowerShellCompletionFile(string) error
}, directory string) error {
	if err := root.GenBashCompletionFile(filepath.Join(directory, "kvdrain.bash")); err != nil {
		return err
	}
	if err := root.GenZshCompletionFile(filepath.Join(directory, "_kvdrain")); err != nil {
		return err
	}
	if err := root.GenFishCompletionFile(filepath.Join(directory, "kvdrain.fish"), true); err != nil {
		return err
	}
	return root.GenPowerShellCompletionFile(filepath.Join(directory, "kvdrain.ps1"))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
