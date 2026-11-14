package inr_test

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/inr"
)

// Assumptions the plan leaves open (research R-08, plan test table): paise are shown only when
// non-zero and never rounded; zero is "₹0"; a negative amount is "-₹…" (sign before the symbol).

func TestFormat_Table(t *testing.T) {
	tests := []struct {
		name  string
		paise int64
		want  string
	}{
		{"zero", 0, "₹0"},
		{"one paisa", 1, "₹0.01"},
		{"five paise keeps leading zero", 5, "₹0.05"},
		{"fifty paise", 50, "₹0.50"},
		{"one rupee", 100, "₹1"},
		{"below grouping", 99900, "₹999"},
		{"first comma", 100000, "₹1,000"},
		{"thousand and five paise", 100005, "₹1,000.05"},
		{"min application upper bound", 2000000, "₹20,000"},
		{"min application lower bound", 1000000, "₹10,000"},
		{"14700 rupees", 1470000, "₹14,700"},
		{"lakh grouping", 12345678, "₹1,23,456.78"},
		{"148500 rupees", 14850000, "₹1,48,500"},
		{"148500 rupees fifty paise", 14850050, "₹1,48,500.50"},
		{"just under a lakh", 9999999, "₹99,999.99"},
		{"exactly a lakh", 10000000, "₹1,00,000"},
		{"ten crore", 1000000000, "₹1,00,00,000"},
		{"ten crore rupees as in plan", 10000000000, "₹10,00,00,000"},
		{"negative", -1470000, "-₹14,700"},
		{"negative paise only", -5, "-₹0.05"},
		{"negative lakh", -12345678, "-₹1,23,456.78"},
		{"max int64", math.MaxInt64, "₹92,23,37,20,36,85,47,758.07"},
		{"min int64", math.MinInt64, "-₹92,23,37,20,36,85,47,758.08"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := inr.Format(tc.paise); got != tc.want {
				t.Fatalf("Format(%d) = %q, want %q", tc.paise, got, tc.want)
			}
		})
	}
}

// TestFormat_Properties: the rendering is lossless (integer-only, no float rounding) and uses
// Indian grouping: the last group of the rupee part has 3 digits, every earlier group has 2.
func TestFormat_Properties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := rapid.Int64().Draw(t, "paise")
		s := inr.Format(p)

		neg := strings.HasPrefix(s, "-")
		body := strings.TrimPrefix(s, "-")
		if !strings.HasPrefix(body, "₹") {
			t.Fatalf("%q: missing rupee sign", s)
		}
		body = strings.TrimPrefix(body, "₹")
		if neg != (p < 0) {
			t.Fatalf("%q: sign does not match %d", s, p)
		}
		rupees, frac, hasFrac := strings.Cut(body, ".")
		if hasFrac && (len(frac) != 2 || frac == "00") {
			t.Fatalf("%q: paise must be two digits and omitted when zero", s)
		}
		groups := strings.Split(rupees, ",")
		for i, g := range groups {
			last := i == len(groups)-1
			switch {
			case last && len(groups) > 1 && len(g) != 3:
				t.Fatalf("%q: last group must have 3 digits", s)
			case !last && i > 0 && len(g) != 2:
				t.Fatalf("%q: inner group must have 2 digits", s)
			case !last && i == 0 && (len(g) < 1 || len(g) > 2):
				t.Fatalf("%q: first group must have 1-2 digits", s)
			}
		}
		// Round trip with big-enough arithmetic: parse digits back as uint64 magnitude.
		digits := strings.ReplaceAll(rupees, ",", "")
		r, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		var c uint64
		if hasFrac {
			c, _ = strconv.ParseUint(frac, 10, 64)
		}
		mag := r*100 + c
		want := uint64(p)
		if p < 0 {
			want = -uint64(p) // two's complement magnitude, correct for MinInt64
		}
		if mag != want {
			t.Fatalf("%q round-trips to %d, want %d", s, mag, want)
		}
	})
}
