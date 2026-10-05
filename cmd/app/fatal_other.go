//go:build !windows

package main

import (
	"fmt"
	"os"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, appName+":", err)
	os.Exit(1)
}
