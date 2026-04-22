// Copyright 2026 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package toml

import (
	"bytes"
	"go/constant"
	"io"
	"math"
	"math/big"
	"strconv"
	"time"

	. "snai.pe/boa/syntax"
)

// ndkSnap records the lexer state at an accepting step.
type ndkSnap struct {
	len     int    // byte-length of l.Token() at acceptance
	pos     Cursor // l.Position at acceptance
	nextPos Cursor // l.NextPosition at acceptance
}

// machine pairs a compiled regexp with the function that emits the final
// token. The captures from the winning match are passed to emit.
type machine struct {
	re   *Regexp
	emit func(l *Lexer, state *lexerState, captures []string) StateFunc
}

// runNDK drives all machines simultaneously on the lexer and emits the token
// produced by the winner (longest accepted prefix).
func (state *lexerState) runNDK(l *Lexer) StateFunc {
	for i := range state.ndkRunners {
		r := &state.ndkRunners[i]
		r.rm.Reset()
		r.cur = r.rm.Step
		r.snap.len = -1
	}

	for {
		r, _, err := l.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return l.Error(err)
		}

		anyAlive := false
		for i := range state.ndkRunners {
			rr := &state.ndkRunners[i]
			if rr.cur == nil {
				continue
			}
			next, accepting := rr.cur(r)
			rr.cur = next
			if next != nil {
				anyAlive = true
				if accepting {
					rr.snap = ndkSnap{l.TokenLen(), l.Position, l.NextPosition}
				}
			}
		}

		if !anyAlive {
			break
		}
	}

	winner := -1
	for i := range state.ndkRunners {
		if state.ndkRunners[i].snap.len >= 0 && (winner < 0 || state.ndkRunners[i].snap.len > state.ndkRunners[winner].snap.len) {
			winner = i
		}
	}
	if winner < 0 {
		return l.Errorf("unexpected character sequence %q", l.Token())
	}

	// Backtrack: push any bytes consumed past the winning snapshot back into the
	// input stream, then truncate the token buffer. PushBack handles arbitrary
	// overshoot depths, unlike repeated UnreadRune (backbuffer capacity = 4).
	snap := state.ndkRunners[winner].snap
	l.PushBack(l.Token()[snap.len:])
	l.TruncateToken(snap.len, snap.pos, snap.nextPos)

	return state.ndkRunners[winner].emit(l, state, state.ndkRunners[winner].rm.Captures(l.Token()))
}

var keyRe = MustCompileRegexp("key", `[a-zA-Z0-9_-]+`)

func keyEmit(l *Lexer, state *lexerState, _ []string) StateFunc {
	l.Emit(TokenIdentifier, l.Token())
	return state.lex
}

var (
	dtDateRe = MustCompileRegexp("date",
		`([0-9]{4})-([0-9]{2})-([0-9]{2})`)

	dtLocalDateTimeRe = MustCompileRegexp("local datetime",
		`([0-9]{4})-([0-9]{2})-([0-9]{2})[Tt ]([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]+))?`)

	dtOffsetDateTimeRe = MustCompileRegexp("offset datetime",
		`([0-9]{4})-([0-9]{2})-([0-9]{2})[Tt ]([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]+))?(?:(Z|z)|([+-])([0-9]{2}):([0-9]{2}))`)

	dtTimeRe = MustCompileRegexp("time",
		`([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]+))?`)
)

// dtParseFrac converts a fractional-seconds string (e.g. "999" or "123456789") to nanoseconds.
// Per TOML spec, precision beyond nanoseconds is truncated.
func dtParseFrac(s string) int {
	nsec, mul := 0, 100_000_000
	for i := 0; i < len(s) && mul > 0; i++ {
		nsec += int(s[i]-'0') * mul
		mul /= 10
	}
	return nsec
}

func dtDateEmit(l *Lexer, state *lexerState, captures []string) StateFunc {
	const (
		gYear = iota + 1
		gMonth
		gDay
	)

	year, _ := strconv.Atoi(captures[gYear])
	month, _ := strconv.Atoi(captures[gMonth])
	day, _ := strconv.Atoi(captures[gDay])

	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if int(t.Month()) != month || t.Day() != day {
		return l.Errorf("invalid date %04d-%02d-%02d", year, month, day)
	}
	l.Emit(TokenDateTime, LocalDate{Year: year, Month: time.Month(month), Day: day})
	return state.lex
}

func dtLocalDateTimeEmit(l *Lexer, state *lexerState, captures []string) StateFunc {
	const (
		gYear = iota + 1
		gMonth
		gDay
		gHour
		gMin
		gSec
		gFrac
	)

	year, _ := strconv.Atoi(captures[gYear])
	month, _ := strconv.Atoi(captures[gMonth])
	day, _ := strconv.Atoi(captures[gDay])
	hour, _ := strconv.Atoi(captures[gHour])
	min, _ := strconv.Atoi(captures[gMin])
	sec, _ := strconv.Atoi(captures[gSec])
	nsec := dtParseFrac(captures[gFrac])

	t := time.Date(year, time.Month(month), day, hour, min, sec, nsec, time.UTC)
	if int(t.Month()) != month || t.Day() != day {
		return l.Errorf("invalid date %04d-%02d-%02d", year, month, day)
	}
	if t.Hour() != hour || t.Minute() != min || t.Second() != sec {
		return l.Errorf("invalid time %02d:%02d:%02d", hour, min, sec)
	}
	l.Emit(TokenDateTime, MakeLocalDateTime(t))
	return state.lex
}

func dtOffsetDateTimeEmit(l *Lexer, state *lexerState, captures []string) StateFunc {
	const (
		gYear = iota + 1
		gMonth
		gDay
		gHour
		gMin
		gSec
		gFrac
		gZulu
		gTZSign
		gTZHour
		gTZMin
	)

	year, _ := strconv.Atoi(captures[gYear])
	month, _ := strconv.Atoi(captures[gMonth])
	day, _ := strconv.Atoi(captures[gDay])
	hour, _ := strconv.Atoi(captures[gHour])
	min, _ := strconv.Atoi(captures[gMin])
	sec, _ := strconv.Atoi(captures[gSec])
	nsec := dtParseFrac(captures[gFrac])

	t := time.Date(year, time.Month(month), day, hour, min, sec, nsec, time.UTC)
	if int(t.Month()) != month || t.Day() != day {
		return l.Errorf("invalid date %04d-%02d-%02d", year, month, day)
	}
	if t.Hour() != hour || t.Minute() != min || t.Second() != sec {
		return l.Errorf("invalid time %02d:%02d:%02d", hour, min, sec)
	}

	var loc *time.Location
	if captures[gZulu] != "" {
		loc = time.UTC
	} else {
		tzH, _ := strconv.Atoi(captures[gTZHour])
		tzM, _ := strconv.Atoi(captures[gTZMin])
		offset := (tzH*60 + tzM) * 60
		if captures[gTZSign] == "-" {
			offset = -offset
		}
		loc = time.FixedZone(captures[gTZSign]+captures[gTZHour]+captures[gTZMin], offset)
	}
	l.Emit(TokenDateTime, time.Date(year, time.Month(month), day, hour, min, sec, nsec, loc))
	return state.lex
}

func dtTimeEmit(l *Lexer, state *lexerState, captures []string) StateFunc {
	const (
		gHour = iota + 1
		gMin
		gSec
		gFrac
	)

	hour, _ := strconv.Atoi(captures[gHour])
	min, _ := strconv.Atoi(captures[gMin])
	sec, _ := strconv.Atoi(captures[gSec])
	nsec := dtParseFrac(captures[gFrac])

	t := time.Date(0, 1, 1, hour, min, sec, nsec, time.UTC)
	if t.Hour() != hour || t.Minute() != min || t.Second() != sec {
		return l.Errorf("invalid time %02d:%02d:%02d", hour, min, sec)
	}
	l.Emit(TokenDateTime, LocalTime{Hour: hour, Minute: min, Second: sec, Nanosecond: nsec})
	return state.lex
}

var (
	decIntRe  = MustCompileRegexp("decimal integer", `[+-]?(?:0|[1-9](?:_?[0-9])*)`)
	floatRe   = MustCompileRegexp("float", `[+-]?(?:0|[1-9](?:_?[0-9])*)(?:\.[0-9](?:_?[0-9])*(?:[eE][+-]?[0-9](?:_?[0-9])*)?|[eE][+-]?[0-9](?:_?[0-9])*)`)
	specialRe = MustCompileRegexp("special float", `[+-](?:inf|nan)`)
	hexIntRe  = MustCompileRegexp("hex integer", `0x[0-9a-fA-F](?:_?[0-9a-fA-F])*`)
	octIntRe  = MustCompileRegexp("octal integer", `0o[0-7](?:_?[0-7])*`)
	binIntRe  = MustCompileRegexp("binary integer", `0b[01](?:_?[01])*`)
)

// decIntEmit parses a decimal integer token, handling the optional sign.
func decIntEmit(l *Lexer, state *lexerState, _ []string) StateFunc {
	val, err := ParseNumberBytes(l.Context, l.TokenBytes(), 512, big.ToNearestEven)
	if err != nil {
		return l.Error(err)
	}
	switch v := val.(type) {
	case int64:
		l.Emit(TokenNumber, constant.MakeInt64(v))
	default:
		l.Emit(TokenNumber, constant.Make(v))
	}
	return state.lex
}

func prefixIntEmit(base int) func(*Lexer, *lexerState, []string) StateFunc {
	// Maximum significant digits for uint64 in this base, used to short-circuit
	// the fast path before any allocation.
	maxDigits := 64 // base 2: 2^64-1 fits in 64 bits
	if base == 8 {
		maxDigits = 22 // 8^22 > MaxUint64 > 8^21
	} else if base == 16 {
		maxDigits = 16 // 16^16-1 = MaxUint64
	}
	return func(l *Lexer, state *lexerState, _ []string) StateFunc {
		tokB := l.TokenBytes()[2:] // skip 0x / 0o / 0b prefix
		// Fast path: accumulate directly over bytes — no string conversion, no allocation.
		// Bail to ParseBigInt on digit-count exceeded or arithmetic overflow.
		var (
			u       uint64
			nDigits int
		)
		for _, b := range tokB {
			if b == '_' {
				continue
			}
			if nDigits++; nDigits > maxDigits {
				goto slow
			}
			var d uint64
			switch {
			case b >= '0' && b <= '9':
				d = uint64(b - '0')
			case b >= 'a' && b <= 'f':
				d = uint64(b-'a') + 10
			case b >= 'A' && b <= 'F':
				d = uint64(b-'A') + 10
			}
			if u > (math.MaxUint64-d)/uint64(base) {
				goto slow
			}
			u = u*uint64(base) + d
		}
		l.Emit(TokenNumber, constant.MakeUint64(u))
		return state.lex
	slow:
		val, err := ParseBigInt(l.Context, bytes.NewReader(tokB), base)
		if err != nil {
			return l.Error(err)
		}
		l.Emit(TokenNumber, constant.Make(val))
		return state.lex
	}
}

func floatEmit(l *Lexer, state *lexerState, _ []string) StateFunc {
	const prec = 512
	val, err := ParseBigFloat(l.Context, bytes.NewReader(l.TokenBytes()), prec, big.ToNearestEven)
	if err != nil {
		return l.Error(err)
	}
	if val.IsInf() {
		// go/constant has no infinity; downgrade to float64.
		l.Emit(TokenNumber, math.Inf(val.Sign()))
	} else {
		l.Emit(TokenNumber, constant.Make(val))
	}
	return state.lex
}

func specialEmit(l *Lexer, state *lexerState, _ []string) StateFunc {
	raw := l.Token()
	switch raw[1:] {
	case "inf":
		sign := 1
		if raw[0] == '-' {
			sign = -1
		}
		l.Emit(TokenNumber, math.Inf(sign))
	case "nan":
		l.Emit(TokenNumber, math.NaN())
	default:
		panic("unreachable")
	}
	return state.lex
}
