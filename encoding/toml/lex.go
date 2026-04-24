// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package toml

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"
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
	expectKey  bool
	ndkRunners []ndkRunner
}

// ndkRunner is a pre-allocated RegexpMachine paired with its machine spec.
// cur and snap are per-run state reset at the start of each runNDK call.
type ndkRunner struct {
	rm   *RegexpMachine
	emit func(l *Lexer, state *lexerState, captures []string) StateFunc
	cur  StepFunc
	snap ndkSnap
}

type pooledLexer struct {
	lx      Lexer
	state   lexerState
	runners []ndkRunner
	done    func()
}

var lexerPool sync.Pool

func init() {
	lexerPool.New = func() any {
		p := new(pooledLexer)
		machines := ndkMachines()
		p.runners = make([]ndkRunner, len(machines))
		for i := range machines {
			p.runners[i].rm = machines[i].re.NewMachine()
			p.runners[i].emit = machines[i].emit
		}
		p.done = func() { lexerPool.Put(p) }
		return p
	}
}

func newLexer(ctx context.Context, input io.Reader) *Lexer {
	p := lexerPool.Get().(*pooledLexer)
	p.state = lexerState{expectKey: true, ndkRunners: p.runners}
	p.lx.Done = p.done
	p.lx.Reinit(ctx, input, p.state.lex)
	return &p.lx
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
		_, err := l.AcceptWhile(isSpace)
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
		comment, err := l.AcceptWhile(func(r rune) bool {
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
					l.AcceptWhile(isSpace)
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
					_, err := l.AcceptWhile(func(r rune) bool {
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
					codepoint, err := l.ParseUnicodeEscape(length)
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

// ndkMachines returns the set of NDK machines used for number/date/key lexing.
// Order matters: on equal-length matches, the first machine wins.
// In particular, decIntRe must precede keyRe so that bare integers
// (e.g. "42") are lexed as numbers, not keys.
func ndkMachines() []machine {
	return []machine{
		{re: floatRe, emit: floatEmit},
		{re: specialRe, emit: specialEmit},
		{re: hexIntRe, emit: prefixIntEmit(16)},
		{re: octIntRe, emit: prefixIntEmit(8)},
		{re: binIntRe, emit: prefixIntEmit(2)},
		{re: decIntRe, emit: decIntEmit},
		{re: dtOffsetDateTimeRe, emit: dtOffsetDateTimeEmit},
		{re: dtLocalDateTimeRe, emit: dtLocalDateTimeEmit},
		{re: dtDateRe, emit: dtDateEmit},
		{re: dtTimeRe, emit: dtTimeEmit},
		{re: keyRe, emit: keyEmit},
	}
}

func (state *lexerState) lexNumberOrDateOrKey(l *Lexer) StateFunc {
	return state.runNDK(l)
}

func (state *lexerState) lexIdentifier(l *Lexer) StateFunc {
	_, err := l.AcceptWhile(isIdentifierChar)
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
