package schema

import (
	"regexp"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// Feature: tenant-environment, Property 1: Tenant name pattern partitions all strings correctly
//
// Validates: Requirements 2.2
//
// For any string s, the compiled tenant pattern accepts s if and only if s is
// 3-22 characters long, every character is lowercase-alphanumeric or a hyphen,
// and s has neither a leading nor a trailing hyphen. The generator produces
// both matching and deliberately invalid strings (uppercase, leading/trailing
// hyphen, too short, too long, empty, embedded illegal characters), and the
// regexp decision must agree with this independently-computed expectation.

// tenantPattern is the tenant name rule from definition.yaml / Requirement 2.2.
const tenantPattern = `^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$`

// expectTenantValid computes, independently of the regexp, whether s should be
// accepted: length 3-22, all chars in [a-z0-9-], and no leading/trailing hyphen.
func expectTenantValid(s string) bool {
	if len(s) < 3 || len(s) > 22 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	if strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
		return false
	}
	return true
}

func TestTenantNamePatternPartition(t *testing.T) {
	re := regexp.MustCompile(tenantPattern)

	// rapid.Check runs many iterations (its default well exceeds the 100
	// minimum required by the task).
	rapid.Check(t, func(t *rapid.T) {
		s := genTenantCandidate().Draw(t, "candidate")

		got := re.MatchString(s)
		want := expectTenantValid(s)

		if got != want {
			t.Fatalf("regexp/expectation disagree for %q: regexp=%v expected=%v", s, got, want)
		}
	})
}

// genTenantCandidate builds a generator that produces a mix of clearly-valid
// tenant names and deliberately invalid ones, so both sides of the accept/
// reject partition are exercised.
func genTenantCandidate() *rapid.Generator[string] {
	// Characters allowed by the pattern's body.
	validRunes := []rune("abcdefghijklmnopqrstuvwxyz0123456789-")
	// Characters the pattern must reject (uppercase, symbols, whitespace).
	invalidRunes := []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ_.!/@ ")

	return rapid.Custom(func(t *rapid.T) string {
		kind := rapid.IntRange(0, 6).Draw(t, "kind")
		switch kind {
		case 0:
			// Well-formed valid name: length 3-22, alnum ends, hyphen allowed inside.
			n := rapid.IntRange(3, 22).Draw(t, "validLen")
			b := make([]rune, n)
			edge := []rune("abcdefghijklmnopqrstuvwxyz0123456789")
			b[0] = edge[rapid.IntRange(0, len(edge)-1).Draw(t, "first")]
			b[n-1] = edge[rapid.IntRange(0, len(edge)-1).Draw(t, "last")]
			for i := 1; i < n-1; i++ {
				b[i] = validRunes[rapid.IntRange(0, len(validRunes)-1).Draw(t, "mid")]
			}
			return string(b)
		case 1:
			// Too short: length 0-2.
			n := rapid.IntRange(0, 2).Draw(t, "shortLen")
			return randRuneString(t, validRunes, n, "short")
		case 2:
			// Too long: length 23-40.
			n := rapid.IntRange(23, 40).Draw(t, "longLen")
			return randRuneString(t, validRunes, n, "long")
		case 3:
			// Leading hyphen.
			rest := randRuneString(t, validRunes, rapid.IntRange(2, 10).Draw(t, "leadLen"), "lead")
			return "-" + rest
		case 4:
			// Trailing hyphen.
			rest := randRuneString(t, validRunes, rapid.IntRange(2, 10).Draw(t, "trailLen"), "trail")
			return rest + "-"
		case 5:
			// Contains at least one illegal character (uppercase/symbol/space).
			n := rapid.IntRange(1, 22).Draw(t, "illegalLen")
			b := make([]rune, n)
			for i := range b {
				b[i] = validRunes[rapid.IntRange(0, len(validRunes)-1).Draw(t, "base")]
			}
			pos := rapid.IntRange(0, n-1).Draw(t, "illegalPos")
			b[pos] = invalidRunes[rapid.IntRange(0, len(invalidRunes)-1).Draw(t, "illegal")]
			return string(b)
		default:
			// Fully arbitrary string, including possibly empty.
			return rapid.String().Draw(t, "arbitrary")
		}
	})
}

// randRuneString draws n runes from the given rune set and concatenates them.
func randRuneString(t *rapid.T, runes []rune, n int, label string) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = runes[rapid.IntRange(0, len(runes)-1).Draw(t, label)]
	}
	return string(b)
}
