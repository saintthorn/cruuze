package server

import "testing"

func TestNormalizeEmail(t *testing.T) {
	valid := map[string]string{
		"ada@example.com":        "ada@example.com",
		"  Ada@Example.COM ":     "ada@example.com",
		"a.b+tag@sub.example.io": "a.b+tag@sub.example.io",
	}
	for in, want := range valid {
		got, ok := normalizeEmail(in)
		if !ok || got != want {
			t.Errorf("normalizeEmail(%q) = %q, %v; want %q, true", in, got, ok, want)
		}
	}

	invalid := []string{
		"",
		"ada",
		"ada@",
		"@example.com",
		"ada@localhost",
		"Ada Lovelace <ada@example.com>",
		"ada@example.com\r\nBcc: eve@example.com",
		"ada@example.com, eve@example.com",
	}
	for _, in := range invalid {
		if got, ok := normalizeEmail(in); ok {
			t.Errorf("normalizeEmail(%q) = %q, true; want rejected", in, got)
		}
	}
}

func TestDeriveTeamKey(t *testing.T) {
	cases := map[string]string{
		"Acme Inc":     "ACM",
		"engineering":  "ENG",
		"QA":           "QA",
		"R2 Unit":      "R2U",
		"2024 Roadmap": "ROA",
		"日本語":          "TEAM",
		"":             "TEAM",
	}
	for name, want := range cases {
		got := deriveTeamKey(name)
		if got != want {
			t.Errorf("deriveTeamKey(%q) = %q, want %q", name, got, want)
		}
		if !teamKeyPattern.MatchString(got) {
			t.Errorf("deriveTeamKey(%q) = %q, which is not a valid team key", name, got)
		}
	}
}

func TestURLKeyPattern(t *testing.T) {
	for _, ok := range []string{"acme", "acme-inc", "a1b", "abc"} {
		if !urlKeyPattern.MatchString(ok) {
			t.Errorf("%q should be a valid URL key", ok)
		}
	}
	for _, bad := range []string{"ab", "-acme", "acme-", "Acme", "ac me", "a/b", ""} {
		if urlKeyPattern.MatchString(bad) {
			t.Errorf("%q should not be a valid URL key", bad)
		}
	}
}
