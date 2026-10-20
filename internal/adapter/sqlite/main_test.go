// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"os"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	if os.Getenv(crashHelperEnv) != "" {
		os.Exit(runCrashHelper())
	}
	goleak.VerifyTestMain(m)
}
