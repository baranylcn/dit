package main

import (
	"os"

	"github.com/happyhackingspace/dit/internal/collect"
)

var version = "dev"

func main() {
	if err := collect.New(version).Run(); err != nil {
		os.Exit(1)
	}
}
