// SPDX-License-Identifier: Apache-2.0

package content

import (
	"regexp"
	"strings"
)

const (
	PresetEmail      = "email"
	PresetPhone      = "phone"
	PresetCreditCard = "credit_card"
	PresetAPIKey     = "api_key"
)

var (
	emailRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
	phoneRE = regexp.MustCompile(
		`\+\d{7,15}|(?:\+\d{1,3}[ .\-]?)?(?:\(\d{1,4}\)[ .\-]?)?\d{2,4}(?:[ .\-]\d{2,4}){1,4}`)
	cardRE   = regexp.MustCompile(`\d(?:[ \-]?\d){12,18}`)
	isoDate  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
	ipv4     = regexp.MustCompile(`^\d{1,3}(?:\.\d{1,3}){3}$`)
	apiKeyRE = regexp.MustCompile(`\bsk-(?:proj-|ant-)?[A-Za-z0-9_\-]{20,}` +
		`|\bgh[pos]_[A-Za-z0-9]{20,}` +
		`|\bgithub_pat_[A-Za-z0-9_]{20,}` +
		`|\bAKIA[0-9A-Z]{16}\b` +
		`|\bxox[abprs]-[A-Za-z0-9\-]{10,}` +
		`|\bAIza[0-9A-Za-z_\-]{35}`)
	bearerRE = regexp.MustCompile(`(\b[Bb]earer[ \t]+)[A-Za-z0-9._~+/\-]{20,}=*`)
)

func presetRules(name string) ([]compiledRule, bool) {
	switch name {
	case PresetEmail:
		return []compiledRule{{re: emailRE, repl: "[EMAIL]", need: needEmail}}, true
	case PresetPhone:
		return []compiledRule{{re: phoneRE, repl: "[PHONE]", accept: acceptPhone, need: needDigits(phoneMinDigits)}}, true
	case PresetCreditCard:
		return []compiledRule{{re: cardRE, repl: "[CREDIT_CARD]", accept: acceptCard, need: needDigits(cardMinDigits)}}, true
	case PresetAPIKey:
		return []compiledRule{
			{re: apiKeyRE, repl: "[API_KEY]", need: needAPIKey},
			{re: bearerRE, repl: "${1}[API_KEY]", need: needBearer},
		}, true
	default:
		return nil, false
	}
}

const (
	phoneMinDigits = 7
	cardMinDigits  = 13
)

var apiKeyLiterals = []string{"sk-", "ghp_", "gho_", "ghs_", "github_pat_", "AKIA", "xox", "AIza"}

func needEmail(s string) bool { return strings.IndexByte(s, '@') >= 0 }

func needAPIKey(s string) bool {
	for _, lit := range apiKeyLiterals {
		if strings.Contains(s, lit) {
			return true
		}
	}
	return false
}

func needBearer(s string) bool { return strings.Contains(s, "earer") }

func needDigits(n int) func(string) bool {
	return func(s string) bool {
		c := 0
		for i := range len(s) {
			if isDigit(s[i]) {
				if c++; c >= n {
					return true
				}
			}
		}
		return false
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isWordByte(c byte) bool {
	return isDigit(c) || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func dashJoined(s string, start, end int) bool {
	return start > 1 && s[start-1] == '-' && isWordByte(s[start-2]) ||
		end+1 < len(s) && s[end] == '-' && isWordByte(s[end+1])
}

func acceptPhone(s string, start, end int) (int, bool) {
	if start > 0 && isWordByte(s[start-1]) || end < len(s) && isWordByte(s[end]) {
		return end, false
	}
	if dashJoined(s, start, end) {
		return end, false
	}
	m := s[start:end]
	if isoDate.MatchString(m) || ipv4.MatchString(m) {
		return end, false
	}
	digits := 0
	for i := range len(m) {
		if isDigit(m[i]) {
			digits++
		}
	}
	return end, digits >= phoneMinDigits && digits <= 15
}

func acceptCard(s string, start, end int) (int, bool) {
	if start > 0 && isDigit(s[start-1]) {
		return end, false
	}
	var ends []int
	for i := start; i < end; i++ {
		if isDigit(s[i]) {
			ends = append(ends, i+1)
		}
	}
	for n := len(ends); n >= cardMinDigits; n-- {
		e := ends[n-1]
		if e < len(s) && isDigit(s[e]) {
			continue
		}
		if luhnValid(s[start:e]) {
			return e, true
		}
	}
	return end, false
}

func luhnValid(s string) bool {
	sum, double := 0, false
	for i := len(s) - 1; i >= 0; i-- {
		if !isDigit(s[i]) {
			continue
		}
		d := int(s[i] - '0')
		if double {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}
