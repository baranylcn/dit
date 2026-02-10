package main

import (
	"os"

	"github.com/happyhackingspace/dit/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.New(version).Run(); err != nil {
		os.Exit(1)
	}
}
