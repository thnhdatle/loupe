// SPDX-License-Identifier: Apache-2.0

// Command loupe is the Loupe server and command line tool.
package main

import (
	"fmt"
	"os"
)

// Set at build time with -ldflags "-X main.version=... -X main.commit=... -X main.buildDate=...".
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "loupe:", err)
		os.Exit(exitCode(err))
	}
}
