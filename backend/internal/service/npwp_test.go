package service

import (
	"testing"

	"github.com/vincommerce/backend/internal/domain"
)

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

func TestValidateVariantMoneyRejectsNonPositivePrices(t *testing.T) {
	// Fractional prices used to be rejected here. They are unrepresentable now
	// (VariantInput carries IDR), so rejection moved to the boundaries that
	// produce input: the handler's toVariantInputs for JSON and ParseInt for
	// CSV rows. This asserts what this layer still owns: positivity.
	cmp := domain.IDR(1500)
	zero := domain.IDR(0)
	cases := []struct {
		name string
		in   VariantInput
		ok   bool
	}{
		{"whole", VariantInput{Price: 1500}, true},
		{"with compare-at", VariantInput{Price: 1500, CompareAtPrice: &cmp}, true},
		{"zero", VariantInput{Price: 0}, false},
		{"negative", VariantInput{Price: -1}, false},
		{"non-positive compare-at", VariantInput{Price: 1500, CompareAtPrice: &zero}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateVariantMoney(tc.in)
			if tc.ok && err != nil {
				t.Fatalf("validateVariantMoney(%+v) = %v", tc.in, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("validateVariantMoney(%+v) accepted it", tc.in)
			}
		})
	}
}
