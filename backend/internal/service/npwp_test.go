package service

import "testing"

func TestNormalizeNPWPAcceptsTheFormatsPeopleType(t *testing.T) {
	cases := map[string]string{
		"12.345.678.9-012.000": "123456789012000", // 15-digit NPWP
		"123456789012000":      "123456789012000",
		"1234567890123456":     "1234567890123456", // 16-digit NIK-as-NPWP
		" 123 456 789 012 000": "123456789012000",
		"":                     "",
	}
	for raw, want := range cases {
		got, err := normalizeNPWP(raw)
		if err != nil {
			t.Errorf("normalizeNPWP(%q) error = %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeNPWP(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestNormalizeNPWPRejectsWrongLength(t *testing.T) {
	for _, raw := range []string{"12345678901234", "12345678901234567", "abc", "12-34"} {
		if _, err := normalizeNPWP(raw); err == nil {
			t.Errorf("normalizeNPWP(%q) accepted a value that is not 15/16 digits", raw)
		}
	}
}
