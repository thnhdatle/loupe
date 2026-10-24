// SPDX-License-Identifier: Apache-2.0

// Command loupe is the Loupe server and command line tool.
package main

import (
	"fmt"
	"os"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// main runs the root command and exits with its status code.
func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "loupe:", err)
		os.Exit(exitCode(err))
	}
}
