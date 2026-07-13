package main

import (
	"os"

	"github.com/stianfro/kvdrain/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
