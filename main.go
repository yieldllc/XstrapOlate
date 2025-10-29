package main

import (
	"os"

	"github.com/drduker/xstrapolate/cmd"
)

var version = "dev"

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
