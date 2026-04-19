// Copyright 2026 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
)

var (
	ErrSyntax = errors.New("syntax error in number")
	ErrRange  = errors.New("number out of range")
)

// chunkSizes[base] is the largest n such that base^n fits in a uint64.
// Only bases actually used by callers (2, 8, 10, 16) are populated.
var chunkSizes = [17]int{
	2:  63, // 2^63
	8:  21, // 8^21
	10: 19, // 10^19 (< 1.8e19 < 2^64)
	16: 15, // 16^15
}

func parseBigInt(ctx context.Context, r io.RuneScanner, base int) (*big.Int, error) {
	chunkN := chunkSizes[base]
	if chunkN == 0 {
		panic(fmt.Sprintf("parseBigInt: unsupported base %d", base))
	}

	z := new(big.Int)
	var (
		mul, add       big.Int
		v, scale       uint64
		hasDigits      bool
		prevUnderscore bool
		done           bool
	)

	// Underscore rules (shared with readDigitsInto): underscores must appear
	// between digits — no leading, trailing, or consecutive underscores.

	for !done {
		v, scale = 0, 1
		n := 0
	chunk:
		for n < chunkN {
			c, _, err := r.ReadRune()
			if err == io.EOF {
				done = true
				break
			}
			if err != nil {
				return nil, err
			}

			if c == '_' {
				if !hasDigits || prevUnderscore {
					return nil, fmt.Errorf("underscore must be between digits: %w", ErrSyntax)
				}
				prevUnderscore = true
				continue
			}

			var d uint64
			switch {
			case c >= '0' && c <= '9' && int(c-'0') < base:
				d = uint64(c - '0')
			case c >= 'a' && c <= 'f' && base == 16:
				d = uint64(c-'a') + 10
			case c >= 'A' && c <= 'F' && base == 16:
				d = uint64(c-'A') + 10
			default:
				if prevUnderscore {
					return nil, fmt.Errorf("underscore must be between digits: %w", ErrSyntax)
				}
				if uerr := r.UnreadRune(); uerr != nil {
					return nil, uerr
				}
				done = true
				break chunk
			}

			prevUnderscore = false
			hasDigits = true
			v = v*uint64(base) + d
			scale *= uint64(base)
			n++
		}

		if n > 0 {
			mul.SetUint64(scale)
			add.SetUint64(v)
			z.Mul(z, &mul)
			z.Add(z, &add)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	if prevUnderscore {
		return nil, fmt.Errorf("underscore must be between digits: %w", ErrSyntax)
	}
	if !hasDigits {
		return nil, fmt.Errorf("empty integer string: %w", ErrSyntax)
	}

	return z, nil
}

// ParseBigInt parses an integer from r in the given base (2, 8, 10, or 16).
// Base 0 auto-detects from a prefix (0x, 0o, 0b, or legacy leading 0) and
// handles an optional leading sign. Reading stops at the first non-digit rune,
// which is unread. Ctx is checked between chunks for cancellation.
func ParseBigInt(ctx context.Context, r io.RuneScanner, base int) (*big.Int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if base != 0 {
		return parseBigInt(ctx, r, base)
	}

	var neg bool
	c1, _, err := r.ReadRune()
	if err == io.EOF {
		return nil, fmt.Errorf("empty integer string: %w", ErrSyntax)
	}
	if err != nil {
		return nil, err
	}
	if c1 == '+' || c1 == '-' {
		neg = c1 == '-'
		c1, _, err = r.ReadRune()
		if err == io.EOF {
			return nil, fmt.Errorf("empty integer string: %w", ErrSyntax)
		}
		if err != nil {
			return nil, err
		}
	}

	if c1 == '0' {
		c2, _, err := r.ReadRune()
		if err == io.EOF {
			return new(big.Int), nil
		}
		if err != nil {
			return nil, err
		}

		switch c2 {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		case '0', '1', '2', '3', '4', '5', '6', '7': // legacy octal
			if uerr := r.UnreadRune(); uerr != nil {
				return nil, uerr
			}
			base = 8
		case '8', '9':
			return nil, fmt.Errorf("digit %q out of range for base 8: %w", c2, ErrSyntax)
		default:
			if uerr := r.UnreadRune(); uerr != nil {
				return nil, uerr
			}
			return new(big.Int), nil
		}
		goto parse
	}

	if uerr := r.UnreadRune(); uerr != nil {
		return nil, uerr
	}
	base = 10

parse:
	v, err := parseBigInt(ctx, r, base)
	if err == nil && neg {
		v.Neg(v)
	}
	return v, err
}

// readDigitsInto reads decimal digits into sb, stopping at the first non-digit
// non-underscore rune. That terminator is consumed and returned as term
// (0 on EOF) so the caller can dispatch on it.
//
// Underscore rules (shared with parseBigInt): underscores must appear between
// digits — no leading, trailing, or consecutive underscores.
func readDigitsInto(r io.RuneScanner, sb *bytes.Buffer) (ndigits int, term rune, err error) {
	var prevUnderscore bool
	for {
		c, _, rerr := r.ReadRune()
		if rerr == io.EOF {
			if prevUnderscore {
				return 0, 0, fmt.Errorf("underscore must be between digits: %w", ErrSyntax)
			}
			// No rune to unread at EOF.
			return ndigits, 0, nil
		}
		if rerr != nil {
			return 0, 0, rerr
		}

		if c == '_' {
			if ndigits == 0 || prevUnderscore {
				return 0, 0, fmt.Errorf("underscore must be between digits: %w", ErrSyntax)
			}
			prevUnderscore = true
			continue
		}

		if c >= '0' && c <= '9' {
			prevUnderscore = false
			sb.WriteByte(byte(c))
			ndigits++
			continue
		}

		if prevUnderscore {
			return 0, 0, fmt.Errorf("underscore must be between digits: %w", ErrSyntax)
		}

		return ndigits, c, nil
	}
}

// readExponent reads an optional sign followed by decimal digits. Returns the
// assembled string (e.g. "-123"). The terminating rune is unread.
func readExponent(r io.RuneScanner) (string, error) {
	var digits bytes.Buffer
	sc, _, err := r.ReadRune()
	if err == io.EOF {
		return "", fmt.Errorf("exponent has no digits: %w", ErrSyntax)
	}
	if err != nil {
		return "", err
	}
	if sc == '+' || sc == '-' {
		digits.WriteByte(byte(sc))
	} else {
		if uerr := r.UnreadRune(); uerr != nil {
			return "", uerr
		}
	}

	ndigits, term, err := readDigitsInto(r, &digits)
	if err != nil {
		return "", err
	}
	if ndigits == 0 {
		return "", fmt.Errorf("exponent has no digits: %w", ErrSyntax)
	}
	if term != 0 {
		if uerr := r.UnreadRune(); uerr != nil {
			return "", uerr
		}
	}
	return digits.String(), nil
}

// ParseBigFloat parses a decimal floating-point number from r at the requested
// precision, truncating the mantissa to significant digits before conversion.
// Reading stops at the first non-float rune, which is unread. Ctx is checked
// between chunks for cancellation.
func ParseBigFloat(ctx context.Context, r io.RuneScanner, prec uint, mode big.RoundingMode) (*big.Float, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	newFloat := func() *big.Float {
		return new(big.Float).SetPrec(prec).SetMode(mode)
	}
	var neg bool
	c, _, err := r.ReadRune()
	if err == io.EOF {
		return nil, fmt.Errorf("invalid decimal float: empty mantissa: %w", ErrSyntax)
	}
	if err != nil {
		return nil, err
	}
	if c == '+' || c == '-' {
		neg = c == '-'
	} else {
		if uerr := r.UnreadRune(); uerr != nil {
			return nil, uerr
		}
	}

	var rawDigits bytes.Buffer
	intDigits, intTerm, err := readDigitsInto(r, &rawDigits)
	if err != nil {
		return nil, err
	}

	var fracDigits int
	mantissaTerm := intTerm
	if intTerm == '.' {
		var fracTerm rune
		fracDigits, fracTerm, err = readDigitsInto(r, &rawDigits)
		if err != nil {
			return nil, err
		}
		mantissaTerm = fracTerm
	}

	if intDigits+fracDigits == 0 {
		return nil, fmt.Errorf("invalid decimal float: empty mantissa: %w", ErrSyntax)
	}

	var expStr string
	if mantissaTerm == 'e' || mantissaTerm == 'E' {
		expStr, err = readExponent(r)
		if err != nil {
			return nil, err
		}
	} else if mantissaTerm != 0 {
		if uerr := r.UnreadRune(); uerr != nil {
			return nil, uerr
		}
	}

	// Skip leading zeros.
	b := rawDigits.Bytes()
	var nzeros int
	for nzeros < len(b) && b[nzeros] == '0' {
		nzeros++
	}
	rawDigits.Next(nzeros)

	if rawDigits.Len() == 0 {
		f := newFloat()
		if neg {
			f.Neg(f)
		}
		return f, nil
	}

	// Cap to the number of significant decimal digits that can affect the
	// rounded result at prec bits. 1233/4096 ~ log10(2).
	var mantissaTrim int
	if maxSigDigits := (int(prec)*1233)>>12 + 4; rawDigits.Len() > maxSigDigits {
		mantissaTrim = rawDigits.Len() - maxSigDigits
		rawDigits.Truncate(maxSigDigits)
	}

	mantissaInt, err := parseBigInt(ctx, &rawDigits, 10)
	if err != nil {
		return nil, fmt.Errorf("invalid decimal float mantissa: %w", err)
	}

	effExp := int64(mantissaTrim) - int64(fracDigits)
	if expStr != "" {
		exp, err := strconv.ParseInt(expStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid exponent %q: %w", expStr, ErrRange)
		}
		sum := effExp + exp
		if (exp > 0 && sum < effExp) || (exp < 0 && sum > effExp) {
			return nil, fmt.Errorf("effective exponent overflow: %w", ErrRange)
		}
		effExp = sum
	}
	if int64(int(effExp)) != effExp {
		return nil, fmt.Errorf("effective exponent %d out of range: %w", effExp, ErrRange)
	}

	f := newFloat().SetInt(mantissaInt)
	if neg {
		f.Neg(f)
	}
	if effExp == 0 {
		return f, nil
	}

	// 10^effExp = 5^effExp * 2^effExp; compute 5^|effExp| by squaring, then
	// apply the 2^effExp half via SetMantExp (adjusts the binary exponent in O(1)).
	absExp := effExp
	if absExp < 0 {
		absExp = -absExp
	}
	if absExp < 0 {
		return nil, fmt.Errorf("effective exponent overflow: %w", ErrRange)
	}
	scale5 := newFloat().SetInt64(1)
	sq5 := newFloat().SetInt64(5)
	for rem := uint64(absExp); rem > 0; rem >>= 1 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if rem&1 != 0 {
			scale5.Mul(scale5, sq5)
		}
		if rem > 1 {
			sq5.Mul(sq5, sq5)
		}
	}
	if effExp > 0 {
		f.Mul(f, scale5)
	} else {
		f.Quo(f, scale5)
	}
	f.SetMantExp(f, int(effExp))
	return f, nil
}
