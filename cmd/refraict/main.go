package main

import (
	"os"

	"github.com/refraict/refraict/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
