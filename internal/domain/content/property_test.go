// SPDX-License-Identifier: Apache-2.0

package content_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"

	"github.com/thnhdatle/loupe/internal/domain/content"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func genParts(t *rapid.T) []trace.ContentPart {
	bodies := rapid.SliceOfN(rapid.String(), 0, 6).Draw(t, "bodies")
	parts := make([]trace.ContentPart, len(bodies))
	for i, b := range bodies {
		parts[i] = trace.ContentPart{
			Part: i, Role: "user", Source: trace.SourceAttribute, Key: "k", Body: []byte(b),
			Bytes: rapid.IntRange(0, 100).Draw(t, "bytes"),
		}
	}
	return parts
}

func TestPolicy_Apply_NoBodyInStrippedModesProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		parts := genParts(t)
		mode := rapid.SampledFrom([]content.Mode{content.ModeOff, content.ModeMetadataOnly}).Draw(t, "mode")
		out, stripped := content.Apply(content.Policy{Mode: mode, MaxBytes: 5}, parts)
		if len(parts) > 0 && !stripped {
			t.Fatal("expected stripped")
		}
		for _, o := range out {
			if o.Body != nil {
				t.Fatalf("body present in mode %s", mode)
			}
		}
	})
}

func TestPolicy_Apply_BudgetAndUTF8Property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		parts := genParts(t)
		limit := rapid.IntRange(1, 40).Draw(t, "limit")
		out, _ := content.Apply(content.Policy{Mode: content.ModeOn, MaxBytes: limit}, parts)
		total := 0
		for i, o := range out {
			total += len(o.Body)
			if !utf8.Valid(o.Body) {
				t.Fatalf("part %d not valid UTF-8: %q", i, o.Body)
			}
			if len(o.Body) < len(parts[i].Body) && !o.Truncated {
				t.Fatalf("part %d shortened but not marked truncated", i)
			}
		}
		if total > limit {
			t.Fatalf("total %d > limit %d", total, limit)
		}
	})
}

var luhnRedactor = func() *content.Redactor {
	r, err := content.NewRedactor([]string{"email", "credit_card"}, nil)
	if err != nil {
		panic(err)
	}
	return r
}()

func luhnCard(digits []int) string {
	sum := 0
	for i := range digits {
		d := digits[len(digits)-1-i]
		if i%2 == 0 {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	var sb strings.Builder
	for _, d := range digits {
		fmt.Fprint(&sb, d)
	}
	fmt.Fprint(&sb, (10-sum%10)%10)
	return sb.String()
}

func TestRedactor_Redact_RemovesEmailAndCardProperty(t *testing.T) {
	t.Parallel()
	letters := rapid.StringMatching(`[a-z]{1,10}`)
	rapid.Check(t, func(t *rapid.T) {
		email := letters.Draw(t, "local") + "@" + letters.Draw(t, "dom") + ".com"
		card := luhnCard(rapid.SliceOfN(rapid.IntRange(0, 9), 12, 18).Draw(t, "digits"))
		pre := rapid.StringMatching(`[a-z ,.]{0,20}`).Draw(t, "pre")
		post := rapid.StringMatching(`[a-z ,.]{0,20}`).Draw(t, "post")
		text := pre + " " + email + " " + card + " " + post
		got := luhnRedactor.Redact(text)
		if strings.Contains(got, email) || strings.Contains(got, card) {
			t.Fatalf("leak in %q (from %q)", got, text)
		}
	})
}
