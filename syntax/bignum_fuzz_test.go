// Copyright 2026 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

//go:build go1.21

package syntax_test

import (
	"math/big"
	"strings"
	"testing"

	"snai.pe/boa/syntax"
)

// FuzzParseBigFloat checks that ParseBigFloat:
//   - never panics for any input
//   - for short inputs without underscores, agrees exactly with big.ParseFloat
//
// f.Context() is cancelled when the fuzzer stops (timeout, -fuzztime, or
// interrupt), so any in-progress parse is cancelled via ctx.Err() and the
// iteration terminates promptly rather than blocking.
func FuzzParseBigFloat(f *testing.F) {
	for _, s := range []string{
		// ordinary values
		"0", "1", "-1", "+1",
		"1.0", "1.5", "-1.5", "0.25",
		"1e2", "1.25e1", "0.5e0",
		// zero variants
		".0", "0.", "0.0", ".0e0",
		// leading/trailing decimal point
		".5", "1.",
		// DoS triggers: large exponent, large mantissa
		"5e5555229", "1e-5555229",
		"1" + strings.Repeat("0", 200),
		// underscore separators (valid)
		"1_000.5", "3_1415927e-7", "1_0e1_0",
		// underscore separators (invalid positions)
		"_1.0", "1.0_", "1_.2", "1._2", "1.2_e3", "1.2e_3",
		// degenerate / invalid
		"", ".", "e", "1e", "+", "-", "++1", "1e1e1",
	} {
		f.Add(s)
	}

	const prec = 256
	ctx := f.Context() // cancelled when the fuzzer stops

	f.Fuzz(func(t *testing.T, s string) {
		reader := strings.NewReader(s)
		got, err := syntax.ParseBigFloat(ctx, reader, prec, big.ToNearestEven)
		if err != nil {
			return
		}
		if got == nil {
			t.Fatalf("nil result with nil error for %q", s)
		}

		// Cross-validate against big.ParseFloat for short, simple inputs.
		// Strings with 'e'/'E' are excluded: big.ParseFloat has no mantissa cap
		// and will materialise the full integer for large exponents (e.g. 5e5555229),
		// defeating the DoS protection we're trying to test. The exponent code path
		// is covered by the unit tests in bignum_test.go instead.
		// Underscores are excluded because big.ParseFloat does not support them.
		// The 30-byte cap ensures the mantissa is never truncated by our parser,
		// so the two implementations must produce bit-for-bit identical results.
		// The reader.Len() == 0 check ensures the full input was consumed; in
		// streaming mode, an invalid suffix causes an early stop without error, so
		// we skip cross-validation in that case.
		if len(s) > 30 || strings.ContainsAny(s, "eE_") || reader.Len() != 0 {
			return
		}
		want, _, wantErr := big.ParseFloat(s, 10, prec, big.ToNearestEven)
		if wantErr != nil {
			// big.ParseFloat rejected the input; our parser accepted it.
			// This is unexpected — report it as a failure.
			t.Fatalf("ParseBigFloat accepted %q but big.ParseFloat rejected it: %v", s, wantErr)
		}
		if got.Cmp(want) != 0 {
			t.Fatalf("ParseBigFloat(%q) = %v, big.ParseFloat = %v", s, got, want)
		}
	})
}

// FuzzParseBigInt checks that ParseBigInt:
//   - never panics for any input
//   - for all inputs it accepts, agrees exactly with big.Int.SetString
func FuzzParseBigInt(f *testing.F) {
	for _, s := range []string{
		// ordinary values
		"0", "1", "123", "9999999999999999999",
		// spans more than one 18-digit chunk
		strings.Repeat("9", 36),
		strings.Repeat("1", 100),
		// invalid inputs — must not panic
		"", "abc", "123abc", "1.5", "-1", "+1", " 1",
	} {
		f.Add(s)
	}

	ctx := f.Context()

	f.Fuzz(func(t *testing.T, s string) {
		reader := strings.NewReader(s)
		got, err := syntax.ParseBigInt(ctx, reader, 10)
		if err != nil {
			return
		}
		if got == nil {
			t.Fatalf("nil result with nil error for %q", s)
		}

		// parseBigIntDecimal accepts only pure decimal digit strings, so
		// big.Int.SetString must accept the same input and produce the same value.
		// Only cross-validate if the full input was consumed (reader.Len() == 0);
		// in streaming mode a non-digit suffix stops parsing without error.
		if reader.Len() != 0 {
			return
		}
		want, wantOk := new(big.Int).SetString(s, 10)
		if !wantOk {
			t.Fatalf("ParseBigInt accepted %q but big.Int.SetString rejected it", s)
		}
		if got.Cmp(want) != 0 {
			t.Fatalf("ParseBigInt(%q) = %v, SetString = %v", s, got, want)
		}
	})
}
