// SPDX-License-Identifier: Apache-2.0

package otlp_test

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
