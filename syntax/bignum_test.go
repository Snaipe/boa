// Copyright 2026 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax_test

import (
	"context"
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"

	"snai.pe/boa/syntax"
)

// cancelAfterCtx implements context.Context, returning nil from Err() for the
// first n calls, then context.Canceled for all subsequent calls. This allows
// deterministic testing of cancellation checkpoints without timing.
type cancelAfterCtx struct {
	n     int // remaining Err() calls that return nil
	calls int // total Err() calls observed
}

func (*cancelAfterCtx) Deadline() (time.Time, bool)   { return time.Time{}, false }
func (*cancelAfterCtx) Done() <-chan struct{}         { return nil }
func (*cancelAfterCtx) Value(interface{}) interface{} { return nil }

func (c *cancelAfterCtx) Err() error {
	c.calls++
	if c.n <= 0 {
		return context.Canceled
	}
	c.n--
	return nil
}

// checkCancellation calls fn with a cancelAfterCtx for n = 0, 1, 2, ...
// until fn succeeds. Every failing invocation must return context.Canceled,
// guaranteeing that every Err() checkpoint in the implementation is exercised.
func checkCancellation(t *testing.T, fn func(ctx context.Context) error) {
	t.Helper()
	for n := 0; n < 1000; n++ {
		ctx := &cancelAfterCtx{n: n}
		err := fn(ctx)
		if err == nil {
			if ctx.calls == 0 {
				t.Fatal("succeeded without ever checking context")
			}
			return
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel after %d: expected context.Canceled, got %v", n, err)
		}
	}
	t.Fatal("did not succeed within 1000 cancellation points")
}

func TestParseBigFloat(t *testing.T) {
	t.Parallel()

	const prec = 256
	parse := func(s string) (*big.Float, error) {
		return syntax.ParseBigFloat(context.Background(), strings.NewReader(s), prec, big.ToNearestEven)
	}

	t.Run("Correctness", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			desc     string
			s        string
			expected float64
		}{
			{"integer", "1.0", 1.0},
			{"one and a half", "1.5", 1.5},
			{"quarter", "0.25", 0.25},
			{"scientific notation", "1e2", 100.0},
			{"uppercase exponent", "1E2", 100.0},
			{"fractional with exponent", "1.25e1", 12.5},
			{"zero exponent", "0.5e0", 0.5},
			{"negative", "-1.5", -1.5},
			{"leading decimal point", ".1", 0.1},
			{"trailing decimal point", "1.", 1},
			{"leading plus with trailing dot", "+1.", 1},
			{"trailing dot before exponent", "1.e10", 1e10},
			{"explicit plus on exponent", "1e+10", 1e10},
			{"leading plus with negative exponent", "+1e-10", 1e-10},
			{"pi approximation", "3.14159265", 3.14159265},
			{"negative with negative exponent", "-687436.79457e-245", -687436.79457e-245},
			{"many leading zeros", ".0000000000000000000000000000000000000001", 1e-40},
			{"many trailing zeros", "+10000000000000000000000000000000000000000e-0", 1e40},
			{"zero with large exponent", "0e100", 0},
			{"zero with large negative exponent", "+0e-100", 0},
			{"negative zero", "-0", math.Copysign(0, -1)},
			{"negative zero with exponent", "-0e+100", math.Copysign(0, -1)},
			{"underscore in integer part", "1_000.5", 1000.5},
			{"underscore in scientific notation", "3_1415927e-7", 3.1415927},
			{"underscore groups", "1_000_000", 1000000},
			{"underscore in mantissa and exponent", "1_0e1_0", 1e11},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				f, err := parse(tc.s)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				actual, _ := f.Float64()
				if actual != tc.expected {
					t.Fatalf("expected %v, actual %v", tc.expected, actual)
				}
				if f.Signbit() != math.Signbit(tc.expected) {
					t.Fatalf("expected signbit %v, actual %v", math.Signbit(tc.expected), f.Signbit())
				}
			})
		}
	})

	// Adapted from math/big's floatconv_test.go.
	// Streaming mode stops at the first unrecognised character and unreads
	// it, so inputs like "2..3" or "0P-0" parse the valid prefix without
	// error; those cases are intentionally omitted here.
	t.Run("Errors", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			desc      string
			s         string
			expectErr error
		}{
			{"empty string", "", syntax.ErrSyntax},
			{"plus sign", "+", syntax.ErrSyntax},
			{"minus sign", "-", syntax.ErrSyntax},
			{"dot only", ".", syntax.ErrSyntax},
			{"dot before exponent", ".e1", syntax.ErrSyntax},
			{"truncated exponent", "1e", syntax.ErrSyntax},
			{"trailing dot truncated exponent", "1.e", syntax.ErrSyntax},
			{"non-digit after exponent marker", "1.2ef", syntax.ErrSyntax},
			{"word infinity", "infinity", syntax.ErrSyntax},
			{"word foobar", "foobar", syntax.ErrSyntax},
			{"underscore after dot", "10._0", syntax.ErrSyntax},
			{"underscore before dot", "1_.2", syntax.ErrSyntax},
			{"underscore before exponent", "1.2_e3", syntax.ErrSyntax},
			{"underscore before exponent digits", "10.0e_0", syntax.ErrSyntax},
			{"trailing underscore in exponent", "10.0e0_", syntax.ErrSyntax},
			{"leading underscore", "_.123", syntax.ErrSyntax},
			{"leading underscore integer part", "_123.456", syntax.ErrSyntax},
			{"trailing underscore in fraction", "1_2.3_4_", syntax.ErrSyntax},
			{"overflowing positive exponent", "1e9223372036854775808", syntax.ErrRange},
			{"overflowing negative exponent on negative", "-1e9223372036854775808", syntax.ErrRange},
			{"overflowing negative exponent", "1e-9223372036854775809", syntax.ErrRange},
			{"overflowing negative exponent on negative 2", "-1e-9223372036854775809", syntax.ErrRange},
			{"MinInt64 exponent", "1e-9223372036854775808", syntax.ErrRange},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				_, err := parse(tc.s)
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !errors.Is(err, tc.expectErr) {
					t.Fatalf("expected %v, got %v", tc.expectErr, err)
				}
			})
		}
	})

	// Verifies that large inputs complete in bounded time: large exponents
	// are handled in O(log(exp)*prec^2) via repeated squaring, and large
	// mantissas are truncated to the significant prefix before parsing.
	t.Run("Large values", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			desc        string
			s           string
			expectedExp int
		}{
			{"large exponent", "5e5555229", 18454074},
			{"large mantissa", "1" + strings.Repeat("0", 9999), 33218},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				// Use higher precision to keep the binary exponent stable
				// for the ±2 assertion below.
				f, err := syntax.ParseBigFloat(ctx, strings.NewReader(tc.s), 512, big.ToNearestEven)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if f.Sign() <= 0 || f.IsInf() {
					t.Fatalf("expected finite positive result, got %v", f)
				}
				exp := f.MantExp(nil)
				if exp < tc.expectedExp-2 || exp > tc.expectedExp+2 {
					t.Fatalf("expected binary exponent near %d, actual %d", tc.expectedExp, exp)
				}
			})
		}
	})

	t.Run("Context cancellation", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			desc string
			s    string
		}{
			{"simple", "1.5"},
			{"with exponent", "5e10"},
			{"multi-chunk mantissa", strings.Repeat("1", 40)},
			{"multi-chunk mantissa with exponent", strings.Repeat("1", 40) + "e10"},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				checkCancellation(t, func(ctx context.Context) error {
					_, err := syntax.ParseBigFloat(ctx, strings.NewReader(tc.s), 256, big.ToNearestEven)
					return err
				})
			})
		}
	})
}

func TestParseBigInt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	parse := func(s string, base int) (*big.Int, error) {
		return syntax.ParseBigInt(ctx, strings.NewReader(s), base)
	}

	t.Run("Correctness", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			desc     string
			in       string
			base     int
			expected string
		}{
			{"zero", "0", 0, "0"},
			{"positive zero", "+0", 0, "0"},
			{"negative zero", "-0", 0, "0"},
			{"negative decimal", "-10", 0, "-10"},
			{"positive decimal", "+10", 0, "10"},
			{"hex lowercase", "0x10", 0, "16"},
			{"negative hex", "-0x10", 0, "-16"},
			{"positive hex", "+0x10", 0, "16"},
			{"hex uppercase", "0X10", 0, "16"},
			{"binary", "0b10", 0, "2"},
			{"binary multi-digit", "0b1001010111", 0, "599"},
			{"negative binary", "-0b111", 0, "-7"},
			{"octal lowercase", "0o10", 0, "8"},
			{"octal uppercase", "0O10", 0, "8"},
			{"legacy octal zero", "00", 0, "0"},
			{"legacy octal", "07", 0, "7"},
			{"legacy octal multi-digit", "023", 0, "19"},
			{"explicit decimal", "10", 10, "10"},
			{"explicit hex", "cafebabe", 16, "3405691582"},
			{"explicit binary", "10", 2, "2"},
			{"explicit octal", "23", 8, "19"},
			{"underscore grouped digits", "1_000", 0, "1000"},
			{"underscore in hex", "-0xF00D_1E", 0, "-15731998"},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				actual, err := parse(tc.in, tc.base)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if actual.Text(10) != tc.expected {
					t.Fatalf("expected %s, actual %s", tc.expected, actual.Text(10))
				}
			})
		}
	})

	t.Run("Errors", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			desc      string
			in        string
			base      int
			expectErr error
		}{
			{"empty base 10", "", 10, syntax.ErrSyntax},
			{"empty base 0", "", 0, syntax.ErrSyntax},
			{"plus sign", "+", 0, syntax.ErrSyntax},
			{"minus sign", "-", 0, syntax.ErrSyntax},
			{"hex prefix only", "0x", 0, syntax.ErrSyntax},
			{"binary prefix only", "0b", 0, syntax.ErrSyntax},
			{"octal prefix only", "0o", 0, syntax.ErrSyntax},
			{"invalid binary digit", "0b2", 0, syntax.ErrSyntax},
			{"invalid legacy octal digit", "08", 0, syntax.ErrSyntax},
			{"invalid octal digit", "0o8", 0, syntax.ErrSyntax},
			{"invalid hex digit", "0xg", 0, syntax.ErrSyntax},
			{"digit out of range base 2", "2", 2, syntax.ErrSyntax},
			{"digit out of range base 8", "8", 8, syntax.ErrSyntax},
			{"digit out of range base 16", "g", 16, syntax.ErrSyntax},
			{"leading underscore", "_1", 0, syntax.ErrSyntax},
			{"trailing underscore", "1_", 0, syntax.ErrSyntax},
			{"double underscore", "-1__0", 0, syntax.ErrSyntax},
			{"trailing underscore before non-digit", "1_x", 10, syntax.ErrSyntax},
			{"underscore after prefix", "0b_1010", 0, syntax.ErrSyntax},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				_, err := parse(tc.in, tc.base)
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !errors.Is(err, tc.expectErr) {
					t.Fatalf("expected %v, got %v", tc.expectErr, err)
				}
			})
		}
	})

	t.Run("Large decimal", func(t *testing.T) {
		t.Parallel()
		// Verifies that a very long decimal integer string is parsed in
		// context-aware chunks so it can be cancelled mid-parse.
		s := "1" + strings.Repeat("0", 9999)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		v, err := syntax.ParseBigInt(ctx, strings.NewReader(s), 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		ref := new(big.Int).Exp(big.NewInt(10), big.NewInt(9999), nil)
		if v.Cmp(ref) != 0 {
			t.Fatal("parsed value does not equal 10^9999")
		}
	})

	t.Run("Context cancellation", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			desc string
			s    string
			base int
		}{
			{"simple", "12345", 10},
			{"multi-chunk", strings.Repeat("1", 40), 10},
			{"hex auto-detect", "0x" + strings.Repeat("f", 20), 0},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				checkCancellation(t, func(ctx context.Context) error {
					_, err := syntax.ParseBigInt(ctx, strings.NewReader(tc.s), tc.base)
					return err
				})
			})
		}
	})
}
