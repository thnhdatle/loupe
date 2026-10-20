// SPDX-License-Identifier: Apache-2.0

// Package content holds the pure content capture, truncation and redaction rules.
//
// Usage:
//
//	redactor, err := content.NewRedactor([]string{"email", "api_key"}, nil)
//	if err != nil {
//		return err
//	}
//	policy := content.Policy{Mode: content.ModeOn, MaxBytes: 32 << 10, Redactor: redactor}
//	out, stripped := content.Apply(policy, parts)
package content
