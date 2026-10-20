// SPDX-License-Identifier: Apache-2.0

package content_test

import (
	"testing"

	"github.com/thnhdatle/loupe/internal/domain/content"
)

// FuzzRedactor checks that the presets never panic. Idempotence is deliberately
// not asserted: replacing one match can remove the word-character context that
// made a neighbouring match invalid (e.g. "AKIA...1 0101111111111111099": the
// card digits are only delimited once the key is replaced), so a second pass
// may legitimately find more.
func FuzzRedactor(f *testing.F) {
	for _, s := range []string{
		"", "bob@example.com", "+1 415-555-0100", "4111 1111 1111 1111",
		"sk-proj-abcdefghijklmnopqrstuvwx", "Bearer abcdefghijklmnopqrstuvwxyz", "2024-01-15 10:30:00",
		"é€\xff", "AKIAIOSFODNN7EXAMPLE 4111111111111111 99",
	} {
		f.Add(s)
	}
	r, err := content.NewRedactor([]string{"email", "phone", "credit_card", "api_key"}, nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_ = r.Redact(s)
		_ = r.RedactBytes([]byte(s))
	})
}
