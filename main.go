package main

import (
	"os"

	"github.com/yieldllc/xstrapolate/cmd"
)

var version = "dev"

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}