// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package toml

import (
	"fmt"
	"go/constant"
	"io"
	"math"
	"math/big"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	. "snai.pe/boa/syntax"
)

const (
	TokenEqual         TokenType = "'='"
	TokenDot           TokenType = "'.'"
	TokenComma         TokenType = "','"
	TokenLBrace        TokenType = "'{'"
	TokenRBrace        TokenType = "'}'"
	TokenLSquare       TokenType = "'['"
	TokenRSquare       TokenType = "']'"
	TokenDoubleLSquare TokenType = "'[['"
	TokenDoubleRSquare TokenType = "']]'"
	TokenDateTime      TokenType = "<datetime>"
)

func isSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

func isNewline(r rune) bool {
	return r == '\n' || r == '\r'
}

func isBadControlChar(r rune) bool {
	return (r >= 0 && r <= 8) || (r > 0x0a && r <= 0x1f) || r == 0x7f
}

func isIdentifierChar(r rune) bool {
	// https://toml.io/en/v1.0.0#keys
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}

type lexerState struct {
	expectKey bool
}

func newLexer(input io.Reader) *Lexer {
	state := lexerState{
		expectKey: true,
	}
	return NewLexer(input, state.lex)
}

func (state *lexerState) acceptNewline(l *Lexer) error {
	r, _, err := l.ReadRune()
	switch {
	case err != nil:
		return err
	case r == '\n':
		return nil
	case r == '\r':
		_, err = l.AcceptRune('\n')
		return err
	}
	return fmt.Errorf("expected '\\n' or '\\r', got %q", r)
}

func (state *lexerState) lex(l *Lexer) StateFunc {
	r, _, err := l.ReadRune()
	if err != nil {
		return l.Error(err)
	}

	switch r {
	case ' ', '\t':
		_, err := l.AcceptUntil(isSpace)
		if err != nil {
			return l.Error(err)
		}
		l.Emit(TokenWhitespace, nil)
	case '{':
		l.Emit(TokenLBrace, nil)
	case '}':
		l.Emit(TokenRBrace, nil)
	case '[':
		next, _, err := l.ReadRune()
		if err != nil {
			return l.Error(err)
		}
		if next == '[' {
			l.Emit(TokenDoubleLSquare, nil)
		} else {
			l.UnreadRune()
			l.Emit(TokenLSquare, nil)
		}
	case ']':
		next, _, err := l.ReadRune()
		if err != nil {
			return l.Error(err)
		}
		if next == ']' {
			l.Emit(TokenDoubleRSquare, nil)
		} else {
			l.UnreadRune()
			l.Emit(TokenRSquare, nil)
		}
	case '=':
		l.Emit(TokenEqual, nil)
	case '.':
		l.Emit(TokenDot, nil)
	case ',':
		l.Emit(TokenComma, nil)
	case '"', '\'':
		l.UnreadRune()
		return state.lexString(l, r)
	// Newlines
	case '\r':
		lf, _, err := l.ReadRune()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return l.Error(err)
		}
		if lf != '\n' {
			return l.Errorf("unexpected character %q: expected LF after CR", lf)
		}
		fallthrough
	case '\n':
		l.Emit(TokenNewline, nil)
	// Comments
	case '#':
		comment, err := l.AcceptUntil(func(r rune) bool {
			return !isBadControlChar(r) && r != '\n'
		})
		if err != nil {
			return l.Error(err)
		}
		err = state.acceptNewline(l)
		if err != nil && err != io.EOF {
			return l.Error(err)
		}
		if err != io.EOF {
			l.UnreadRune()
		}
		l.Emit(TokenComment, strings.TrimSpace(comment))
	case '+', '-', '_', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		// For the most part, a significant set of valid numbers and dates
		// are also valid keys, which means we have to do this in two parts.
		// First, lex until the first non-number-date-key character, and
		// then disambiguate.
		l.UnreadRune()
		return state.lexNumberOrDateOrKey
	default:
		if isIdentifierChar(r) {
			l.UnreadRune()
			return state.lexIdentifier
		}
		return l.Errorf("unexpected character %q", r)
	}
	return state.lex
}

func parseUnicodeEscape(l *Lexer, length int) (rune, error) {
	var codepoint rune
	for i := 0; i < length; i++ {
		r, err := l.AcceptFunc(func(r rune) bool {
			return strings.IndexRune("0123456789abcdefABCDEF", r) != -1
		})
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return 0, fmt.Errorf("invalid unicode escape sequence: %w", err)
		}

		var digit rune
		switch {
		case r >= 'a':
			digit = 10 + r - 'a'
		case r >= 'A':
			digit = 10 + r - 'A'
		case r >= '0':
			digit = r - '0'
		default:
			panic("programming error: got non-hex character")
		}
		codepoint = codepoint | (digit << (4 * int32(length-i-1)))
	}
	if uint32(codepoint) >= uint32(unicode.MaxRune) {
		return 0, fmt.Errorf("invalid unicode escape sequence: max rune is \\U0010FFFF")
	}
	if !utf8.ValidRune(codepoint) {
		return 0, fmt.Errorf("invalid unicode escape sequence: rune is not representable in UTF-8")
	}
	return codepoint, nil
}

func (state *lexerState) lexString(l *Lexer, delim rune) StateFunc {
	return func(l *Lexer) StateFunc {
		var val strings.Builder
		if _, err := l.AcceptRune(delim); err != nil {
			return l.Error(err)
		}

		literal := delim == '\''

		var multiline bool

		r, _, err := l.ReadRune()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return l.Error(err)
		}
		if r == delim {
			r, _, err := l.ReadRune()
			if err != nil && err != io.EOF {
				return l.Error(err)
			}
			if r != delim {
				if err == nil {
					l.UnreadRune()
				}
				// This is an empty string literal
				l.Emit(TokenString, "")
				return state.lex
			}
			// This is a multiline string literal
			multiline = true
		} else {
			l.UnreadRune()
		}

		firstnl := true
		for {
			r, _, err := l.ReadRune()
			if err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return l.Error(err)
			}
			if isBadControlChar(r) || !utf8.ValidRune(r) {
				return l.Errorf("invalid character %q", r)
			}

			switch r {
			case delim:
				if multiline {
					// Multiline strings allow up to two consecutive unescaped
					// delimiters.
					ndelim := 1
					for ; ndelim < 5; ndelim++ {
						var err error
						r, _, err = l.ReadRune()
						if err != nil {
							if err == io.EOF {
								err = io.ErrUnexpectedEOF
							}
							return l.Error(err)
						}
						if r != delim {
							l.UnreadRune()
							break
						}
					}
					if ndelim < 3 {
						for i := 0; i < ndelim; i++ {
							val.WriteRune(delim)
						}
						break
					}

					// 3 or more delimiters mark the end of the multiline string
					for i := 0; i < ndelim-3; i++ {
						val.WriteRune(delim)
					}
				}
				l.Emit(TokenString, val.String())
				return state.lex
			case '\n', '\r':
				if !multiline {
					return l.Errorf("unexpected newline")
				}
				// The first newline is trimmed in multiline mode
				if r == '\r' {
					if _, err := l.AcceptRune('\n'); err != nil {
						return l.Error(err)
					}
					if !firstnl {
						val.WriteRune(r)
					}
					r = '\n'
				}
				if !firstnl {
					val.WriteRune(r)
				}
			case '\\':
				if literal {
					val.WriteRune(r)
					break
				}
				next, _, err := l.ReadRune()
				if err != nil {
					if err == io.EOF {
						err = io.ErrUnexpectedEOF
					}
					return l.Error(err)
				}

				switch next {
				case '\\', delim:
					val.WriteRune(next)
				case ' ', '\t':
					// Whitespace is only allowed after a \ before a newline
					l.AcceptUntil(isSpace)
					r, _, err = l.ReadRune()
					if err != nil {
						return l.Error(err)
					}
					if r != '\r' && r != '\n' {
						return l.Errorf("An unescaped backslash can only have whitespace after it until the end of the line")
					}
					fallthrough
				case '\r', '\n':
					if r == '\r' {
						if _, err := l.AcceptRune('\n'); err != nil {
							return l.Error(err)
						}
					}
					// Skip until the first non-space, non-newline character
					_, err := l.AcceptUntil(func(r rune) bool {
						return isSpace(r) || isNewline(r)
					})
					if err != nil {
						return l.Error(err)
					}
				case 'b':
					val.WriteRune('\b')
				case 'f':
					val.WriteRune('\f')
				case 'n':
					val.WriteRune('\n')
				case 'r':
					val.WriteRune('\r')
				case 't':
					val.WriteRune('\t')
				case 'u', 'U':
					length := 4
					if next == 'U' {
						length = 8
					}
					codepoint, err := parseUnicodeEscape(l, length)
					if err != nil {
						return l.Error(err)
					}
					val.WriteRune(codepoint)
				default:
					return l.Errorf("invalid escape sequence '\\%c'", next)
				}
			default:
				val.WriteRune(r)
			}
			firstnl = false
		}
	}
}

const (
	rinteger  = `(?:(?:0(?:b[01][01_]*|o[0-7][0-7_]*|x[0-9a-fA-F][0-9a-fA-F_]*)?)|[-+]?[1-9][0-9_]*)`
	rnumber   = `(` + rinteger + `|[-+]?(?:inf|nan|(?:0|[1-9][0-9_]*)(?:\.[0-9][0-9_]*)?(?:[eE][-+]?[0-9][0-9_]*)?))`
	rdate     = `(?:[0-9]+\-[0-9]+\-[0-9]+)`
	rtime     = `(?:[0-9]{2}\:[0-9]{2}\:[0-9]{2}(?:\.[0-9]+)?)`
	rdatetime = `(` + rdate + `[ ]?|` + rtime + `|` + rdate + `[tT ]` + rtime + `(?:[zZ]|[zZ+-][0-9]{2}:[0-9]{2})?)`
	rkey      = `([a-zA-Z0-9_-]+)`
)

var reNDK = MustCompileRegexp("number, date, or key", `(?m:^(?:`+rnumber+`|`+rdatetime+`|`+rkey+`))`)

func (state *lexerState) lexNumberOrDateOrKey(l *Lexer) StateFunc {
	ndk, err := reNDK.Accept(l)
	if err != nil {
		return l.Error(err)
	}

	// reject groups that do not take the length of the full match
	for i := 1; i < len(ndk); i++ {
		if len(ndk[i]) != len(ndk[0]) {
			ndk[i] = ""
		}
	}

	const (
		// regexp group indices
		number = 1 + iota
		datetime
		key
	)

	switch {
	case ndk[number] != "":
		num := ndk[number]
		switch {
		case strings.HasSuffix(num, "nan"):
			l.Emit(TokenNumber, math.NaN())
		case strings.HasSuffix(num, "inf"):
			sign := 1
			if num[0] == '-' {
				sign = -1
			}
			l.Emit(TokenNumber, math.Inf(sign))
		case (len(num) < 2 || num[0] != '0' || strings.IndexByte("obx", num[1]) == -1) && strings.ContainsAny(num[1:], "eE+-."):
			const prec = 512 // matches current implementation of go/constant

			if strings.ContainsAny(num, "obx") {
				// should never happen; this was caught by
				return l.Errorf("parsing '%v': invalid float", num)
			}

			// big.ParseFloat supports all cases we care about.
			val, _, err := big.ParseFloat(num, 0, prec, big.ToNearestEven)
			if err != nil {
				return l.Error(err)
			}
			if val.IsInf() {
				l.Emit(TokenNumber, math.Inf(val.Sign()))
			} else {
				constv := constant.Make(val)
				if constv.Kind() != constant.Float {
					panic("created float constant is not float")
				}
				l.Emit(TokenNumber, constv)
			}
		default:
			val, ok := new(big.Int).SetString(num, 0)
			if !ok {
				return l.Errorf("parsing '%v': invalid integer", num)
			}
			constv := constant.Make(val)
			if constv.Kind() != constant.Int {
				panic("created int constant is not int")
			}
			l.Emit(TokenNumber, constv)
		}
	case ndk[datetime] != "":
		datetime := ndk[datetime]

		type layout struct {
			layout  string
			convert func(time.Time) interface{}
		}

		// normalize the date; everything to uppercase, replace space with T
		datetime = strings.Map(func(r rune) rune {
			if r == ' ' {
				return 'T'
			}
			return unicode.ToUpper(r)
		}, strings.TrimRight(datetime, " "))

		// Try these layouts, in order.
		layouts := []layout{
			{"2006-01-02T15:04:05.999999999Z07:00", nil},                                                     // RFC3339
			{"2006-01-02T15:04:05.999999999-07:00", nil},                                                     // RFC3339, with sign instead of Z
			{"2006-01-02T15:04:05.999999999", func(t time.Time) interface{} { return MakeLocalDateTime(t) }}, // RFC3339, without timezone
			{"2006-01-02", func(t time.Time) interface{} { return MakeLocalDate(t) }},                        // RFC3339, without time & timezone
			{"15:04:05.999999999", func(t time.Time) interface{} { return MakeLocalTime(t) }},                // RFC3339, without date & timezone
		}

		var firsterr error
		for _, lay := range layouts {
			val, err := time.Parse(lay.layout, datetime)
			if err != nil {
				if firsterr == nil {
					firsterr = err
				}
				continue
			}
			if lay.convert != nil {
				switch out := lay.convert(val).(type) {
				case LocalDateTime:
					l.Emit(TokenDateTime, out)
				case LocalDate:
					l.Emit(TokenDateTime, out)
				case LocalTime:
					l.Emit(TokenDateTime, out)
				default:
					panic("unexpected time layout type")
				}
			} else {
				l.Emit(TokenDateTime, val)
			}
			return state.lex
		}
		if firsterr == nil {
			firsterr = fmt.Errorf("invalid datetime %s", datetime)
		}
		return l.Error(firsterr)
	case ndk[key] != "":
		l.Emit(TokenIdentifier, ndk[key])
	default:
		panic("no groups matched but syntax.Regexp.Accept did not return an error")
	}

	return state.lex
}

func (state *lexerState) lexIdentifier(l *Lexer) StateFunc {
	_, err := l.AcceptUntil(isIdentifierChar)
	if err != nil {
		return l.Error(err)
	}

	switch val := l.Token(); val {
	case "inf":
		l.Emit(TokenNumber, math.Inf(1))
	case "nan":
		l.Emit(TokenNumber, math.NaN())
	case "true":
		l.Emit(TokenBool, true)
	case "false":
		l.Emit(TokenBool, false)
	default:
		l.Emit(TokenIdentifier, val)
	}
	return state.lex
}

type LocalDate struct {
	Year  int
	Month time.Month
	Day   int
}

func MakeLocalDate(t time.Time) LocalDate {
	return LocalDate{Year: t.Year(), Month: t.Month(), Day: t.Day()}
}

func (date LocalDate) Time(hour, min, sec, nsec int, loc *time.Location) time.Time {
	return time.Date(date.Year, date.Month, date.Day, hour, min, sec, nsec, loc)
}

func (date LocalDate) String() string {
	if date.Year == 0 {
		date.Year = 1
	}
	if date.Month <= 0 {
		date.Month = 1
	}
	if date.Day <= 0 {
		date.Day = 1
	}
	return fmt.Sprintf("%04d-%02d-%02d", date.Year, date.Month, date.Day)
}

type LocalTime struct {
	Hour       int
	Minute     int
	Second     int
	Nanosecond int
}

func MakeLocalTime(t time.Time) LocalTime {
	return LocalTime{Hour: t.Hour(), Minute: t.Minute(), Second: t.Second(), Nanosecond: t.Nanosecond()}
}

func (t LocalTime) Time(year int, month time.Month, day int, loc *time.Location) time.Time {
	return time.Date(year, month, day, t.Hour, t.Minute, t.Second, t.Nanosecond, loc)
}

func (t LocalTime) String() string {
	if t.Nanosecond == 0 {
		return fmt.Sprintf("%02d:%02d:%02d", t.Hour, t.Minute, t.Second)
	}
	return fmt.Sprintf("%02d:%02d:%02d.%09d", t.Hour, t.Minute, t.Second, t.Nanosecond)
}

type LocalDateTime struct {
	LocalDate
	LocalTime
}

func MakeLocalDateTime(t time.Time) LocalDateTime {
	return LocalDateTime{LocalDate: MakeLocalDate(t), LocalTime: MakeLocalTime(t)}
}

func (dt LocalDateTime) Time(loc *time.Location) time.Time {
	return time.Date(dt.Year, dt.Month, dt.Day, dt.Hour, dt.Minute, dt.Second, dt.Nanosecond, loc)
}

func (dt LocalDateTime) String() string {
	return dt.LocalDate.String() + "T" + dt.LocalTime.String()
}
