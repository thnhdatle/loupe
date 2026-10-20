// SPDX-License-Identifier: Apache-2.0

package content_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/content"
)

func mustRedactor(t *testing.T, presets []string, custom []content.Rule) *content.Redactor {
	t.Helper()
	r, err := content.NewRedactor(presets, custom)
	require.NoError(t, err)
	return r
}

func TestRedactor_Redact_Presets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		preset string
		in     string
		want   string
	}{
		{"email simple", "email", "mail bob@example.com now", "mail [EMAIL] now"},
		{"email plus subdomain", "email", "a.b+tag@mail.example.co.uk!", "[EMAIL]!"},
		{"email two", "email", "a@b.io, c@d.org", "[EMAIL], [EMAIL]"},
		{"email none at sign", "email", "no at sign.example.com", "no at sign.example.com"},
		{"email no tld", "email", "user@localhost", "user@localhost"},
		{"phone us dashes plus", "phone", "call +1 415-555-0100 today", "call [PHONE] today"},
		{"phone us parens", "phone", "call (415) 555-0100.", "call [PHONE]."},
		{"phone uk", "phone", "+44 20 7946 0958", "[PHONE]"},
		{"phone plain e164", "phone", "tel +14155550100", "tel [PHONE]"},
		{"phone dots", "phone", "415.555.0100", "[PHONE]"},
		{"phone short number", "phone", "order 12345 and 123-45", "order 12345 and 123-45"},
		{"phone six digits", "phone", "code 123-456", "code 123-456"},
		{"phone iso date", "phone", "on 2024-01-15 we shipped", "on 2024-01-15 we shipped"},
		{"phone iso datetime", "phone", "at 2024-01-15 10:30:00", "at 2024-01-15 10:30:00"},
		{"phone hex id", "phone", "id a1b2c3d4-1234-5678-9012-abcdef123456", "id a1b2c3d4-1234-5678-9012-abcdef123456"},
		{"phone long digits", "phone", "4111111111111111", "4111111111111111"},
		{"phone ipv4", "phone", "host 192.168.100.200 up", "host 192.168.100.200 up"},
		{"phone version", "phone", "go1.27.2 v3.14", "go1.27.2 v3.14"},
		{"card visa", "credit_card", "pay 4111111111111111 ok", "pay [CREDIT_CARD] ok"},
		{"card spaces", "credit_card", "4111 1111 1111 1111", "[CREDIT_CARD]"},
		{"card dashes", "credit_card", "4111-1111-1111-1111.", "[CREDIT_CARD]."},
		{"card amex 15", "credit_card", "378282246310005", "[CREDIT_CARD]"},
		{"card luhn invalid", "credit_card", "4111111111111112", "4111111111111112"},
		{"card too short", "credit_card", "411111111111", "411111111111"},
		{"card 20 digits", "credit_card", "41111111111111111111", "41111111111111111111"},
		{"card trailing number", "credit_card", "4111111111111111 99", "[CREDIT_CARD] 99"},
		{"api openai", "api_key", "key sk-abcdefghijklmnopqrstuvwx end", "key [API_KEY] end"},
		{"api proj", "api_key", "sk-proj-abcdefghijklmnopqrstuvwx", "[API_KEY]"},
		{"api anthropic", "api_key", "sk-ant-api03-abcdefghijklmnopqrstuv", "[API_KEY]"},
		{"api short sk", "api_key", "sk-short", "sk-short"},
		{"api github", "api_key", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", "[API_KEY]"},
		{"api github pat", "api_key", "github_pat_11ABCDEFG0abcdefghijklmn", "[API_KEY]"},
		{"api aws", "api_key", "AKIAIOSFODNN7EXAMPLE", "[API_KEY]"},
		{"api aws wrong length", "api_key", "AKIAIOSFODNN7EXAMPL", "AKIAIOSFODNN7EXAMPL"}, // gitleaks:allow
		{"api slack", "api_key", "xoxb-1234567890-abcdefghij", "[API_KEY]"},               // gitleaks:allow
		{"api google", "api_key", "AIzaSyA1234567890abcdefghijklmnopqrstuv", "[API_KEY]"}, // gitleaks:allow
		{"api bearer", "api_key", "Authorization: Bearer abcdef0123456789abcdef.xyz", "Authorization: Bearer [API_KEY]"},
		{"api bearer short", "api_key", "Bearer short", "Bearer short"},
		{"api plain text", "api_key", "the task-force asked", "the task-force asked"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := mustRedactor(t, []string{tc.preset}, nil)
			assert.Equal(t, tc.want, r.Redact(tc.in))
			assert.Equal(t, tc.want, string(r.RedactBytes([]byte(tc.in))))
		})
	}
}

func TestRedactor_Redact_CustomAndOrder(t *testing.T) {
	t.Parallel()
	r := mustRedactor(t, []string{"email"}, []content.Rule{
		{Name: "ticket", Pattern: `TCK-(\d+)`, Replacement: "ticket#$1"},
		{Name: "mask", Pattern: `\[EMAIL\]`, Replacement: "<mail>"},
	})
	assert.Equal(t, "<mail> ticket#12 -> ticket#12", r.Redact("a@b.io TCK-12 -> TCK-12"))
}

func TestRedactor_NewRedactor_AggregatesErrors(t *testing.T) {
	t.Parallel()
	r, err := content.NewRedactor([]string{"email", "nope"}, []content.Rule{
		{Name: "", Pattern: "x"},
		{Name: "bad", Pattern: "("},
		{Name: "nopattern"},
		{Name: "ok", Pattern: "y"},
	})
	assert.Nil(t, r)
	require.ErrorIs(t, err, content.ErrInvalidRule)
	for _, want := range []string{`unknown preset "nope"`, "empty name", `rule "bad"`, "empty pattern"} {
		assert.ErrorContains(t, err, want)
	}
}

func TestRedactor_Redact_NilAndEmpty(t *testing.T) {
	t.Parallel()
	var nilR *content.Redactor
	assert.Equal(t, "a@b.io", nilR.Redact("a@b.io"))
	assert.Equal(t, []byte("a@b.io"), nilR.RedactBytes([]byte("a@b.io")))

	empty := mustRedactor(t, nil, nil)
	assert.Equal(t, "a@b.io", empty.Redact("a@b.io"))
	in := []byte("a@b.io")
	assert.Same(t, &in[0], &empty.RedactBytes(in)[0])

	full := mustRedactor(t, []string{"email"}, nil)
	assert.Empty(t, full.RedactBytes(nil))
	clean := []byte("nothing here")
	assert.Same(t, &clean[0], &full.RedactBytes(clean)[0])
}

//nolint:paralleltest // testing.AllocsPerRun panics in parallel tests.
func TestRedactor_Redact_NoAllocWithoutRules(t *testing.T) {
	var nilR *content.Redactor
	s := "hello a@b.io"
	allocs := testing.AllocsPerRun(10, func() { _ = nilR.Redact(s) })
	assert.Zero(t, allocs)
}
