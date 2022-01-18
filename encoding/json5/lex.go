// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package json5

import (
	"fmt"
	"go/constant"
	"io"
	"math"
	"math/big"
	"strings"
	"unicode"

	. "snai.pe/boa/syntax"
)

const (
	TokenColon   TokenType = "':'"
	TokenComma   TokenType = "','"
	TokenLBrace  TokenType = "'{'"
	TokenRBrace  TokenType = "'}'"
	TokenLSquare TokenType = "'['"
	TokenRSquare TokenType = "']'"
	TokenPlus    TokenType = "'+'"
	TokenMinus   TokenType = "'-'"
)

const (
	lineSep = '\u2028'
	parSep  = '\u2029'
)

type lexerState struct {
	blankLine bool
}

func newLexer(input io.Reader) *Lexer {
	state := lexerState{
		blankLine: true,
	}
	return NewLexer(input, state.lex)
}

func (state *lexerState) lex(l *Lexer) StateFunc {
	r, _, err := l.ReadRune()
	if err != nil {
		return l.Error(err)
	}

	blankLine := false
	defer func() {
		state.blankLine = blankLine
	}()

	switch r {
	case '{':
		l.Emit(TokenLBrace, nil)
	case '}':
		l.Emit(TokenRBrace, nil)
	case '[':
		l.Emit(TokenLSquare, nil)
	case ']':
		l.Emit(TokenRSquare, nil)
	case ':':
		l.Emit(TokenColon, nil)
	case ',':
		l.Emit(TokenComma, nil)
	case '+':
		l.Emit(TokenPlus, nil)
	case '-':
		l.Emit(TokenMinus, nil)
	case '"', '\'':
		l.UnreadRune()
		return state.lexString(l, r)
	// Newlines
	case '\n', lineSep, parSep:
		l.Emit(TokenNewline, nil)
	case '\r':
		lf, _, err := l.ReadRune()
		if err != nil {
			if err == io.EOF {
				l.Emit(TokenNewline, nil)
			}
			return l.Error(err)
		}
		if lf != '\n' {
			l.UnreadRune()
		}
		l.Emit(TokenNewline, nil)
	// Numbers
	case '0':
		next, _, err := l.ReadRune()
		if err != nil {
			if err == io.EOF {
				l.Emit(TokenNumber, constant.Make(0))
			}
			return l.Error(err)
		}
		switch next {
		case 'x', 'X':
			return state.lexHex
		case '.', 'e', 'E':
			l.UnreadRune()
			l.UnreadRune()
			return state.lexNumber
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			// Numbers can't start with 0
			return l.Errorf("parsing integer: unexpected character '%c'", next)
		default:
			l.Emit(TokenNumber, constant.Make(0))
			return state.lex
		}
	case '.', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		l.UnreadRune()
		return state.lexNumber
	// Comments
	case '/':
		next, _, err := l.ReadRune()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return l.Error(err)
		}
		switch next {
		case '/':
			comment, err := l.AcceptUntil(func(r rune) bool {
				return r != '\n' && r != '\r' && r != lineSep && r != parSep
			})
			if err != nil && err != io.EOF {
				return l.Error(err)
			}
			typ := TokenComment
			if !state.blankLine {
				typ = TokenInlineComment
			}
			l.Emit(typ, strings.TrimSpace(comment))
		case '*':
			// preserve blankLine state, which would otherwise be reset
			blankLine = state.blankLine
			return state.lexBlockComment
		default:
			return l.Errorf("unexpected character %q", next)
		}
	default:
		if unicode.IsSpace(r) {
		loop:
			for {
				r, _, err := l.ReadRune()
				if err != nil {
					if err == io.EOF {
						l.Emit(TokenWhitespace, nil)
					}
					return l.Error(err)
				}
				switch r {
				case '\n', '\r', lineSep, parSep:
					l.UnreadRune()
					state.blankLine = true
					break loop
				}
				if !unicode.IsSpace(r) {
					l.UnreadRune()
					break loop
				}
			}
			l.Emit(TokenWhitespace, nil)
			blankLine = state.blankLine
			return state.lex
		}

		if isIdentifierChar(r, 0) {
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

		for {
			r, _, err := l.ReadRune()
			if err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return l.Error(err)
			}

			switch r {
			case delim:
				l.Emit(TokenString, val.String())
				return state.lex
			case '\n', '\r', lineSep, parSep:
				return l.Errorf("unexpected newline")
			case '\\':
				next, _, err := l.ReadRune()
				if err != nil {
					if err == io.EOF {
						err = io.ErrUnexpectedEOF
					}
					return l.Error(err)
				}

				switch next {
				case '\n', lineSep, parSep, '\\', delim:
					val.WriteRune(next)
				case '\r':
					val.WriteRune(next)
					lf, _, err := l.ReadRune()
					if err != nil {
						if err == io.EOF {
							err = io.ErrUnexpectedEOF
						}
						return l.Error(err)
					}

					switch lf {
					case '\n':
						val.WriteRune(lf)
					default:
						l.UnreadRune()
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
				case 'u':
					codepoint, err := parseUnicodeEscape(l)
					if err != nil {
						return l.Error(err)
					}
					val.WriteRune(codepoint)
				}
			default:
				val.WriteRune(r)
			}
		}
	}
}

func parseUnicodeEscape(l *Lexer) (rune, error) {
	var codepoint rune
	for i := 0; i < 4; i++ {
		r, err := l.AcceptFunc(func(r rune) bool {
			return strings.IndexRune("0123456789abcdefABCDEF", r) != -1
		})
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
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
		codepoint = codepoint | (digit << (4 * (3 - int32(i))))
	}
	return codepoint, nil
}

func isIdentifierChar(r rune, i int) bool {
	// https://262.ecma-international.org/5.1/#sec-7.6
	ok := unicode.IsLetter(r) || r == '$' || r == '_' || r == '\\'
	if i > 0 {
		ok = ok || unicode.In(r, unicode.Nl, unicode.Nd, unicode.Mn, unicode.Mc, unicode.Pc) || r == '\u200C' || r == '\u200D'
	}
	return ok
}

func (state *lexerState) lexIdentifier(l *Lexer) StateFunc {
	var ident strings.Builder
	i := 0
	for {
		r, _, err := l.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return l.Error(err)
		}

		if !isIdentifierChar(r, i) {
			l.UnreadRune()
			break
		}

		if r == '\\' {
			if _, err := l.AcceptRune('u'); err != nil {
				return l.Error(err)
			}
			codepoint, err := parseUnicodeEscape(l)
			if err != nil {
				return l.Error(err)
			}
			ident.WriteRune(codepoint)
		} else {
			ident.WriteRune(r)
		}
		i++
	}

	switch l.Token() {
	case "Infinity":
		l.Emit(TokenNumber, constant.MakeFloat64(math.Inf(1)))
	case "NaN":
		l.Emit(TokenNumber, constant.MakeFloat64(math.NaN()))
	case "null":
		l.Emit(TokenNil, nil)
	case "true":
		l.Emit(TokenBool, true)
	case "false":
		l.Emit(TokenBool, false)
	default:
		l.Emit(TokenIdentifier, ident.String())
	}
	return state.lex
}

func (state *lexerState) lexBlockComment(l *Lexer) StateFunc {
	for {
		r, _, err := l.ReadRune()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return l.Error(err)
		}

		if r == '*' {
			next, _, err := l.ReadRune()
			if err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return l.Error(err)
			}
			if next == '/' {
				tok := l.Token()
				l.Emit(TokenComment, strings.TrimSpace(tok[2:len(tok)-2]))
				return state.lex
			}
			l.UnreadRune()
		}
	}
}

func (state *lexerState) lexHex(l *Lexer) StateFunc {
	num, err := l.AcceptUntil(func(r rune) bool {
		return unicode.In(r, unicode.L, unicode.N)
	})
	if err != nil {
		return l.Error(err)
	}
	val, ok := new(big.Int).SetString(num, 16)
	if !ok {
		return l.Errorf("parsing %v: invalid hexadecimal integer", num)
	}
	l.Emit(TokenNumber, constant.Make(val))
	return state.lex
}

func (state *lexerState) lexNumber(l *Lexer) StateFunc {
	num, err := l.AcceptUntil(func(r rune) bool {
		return strings.IndexRune("0123456789eE+-.", r) != -1
	})
	if err != nil {
		return l.Error(err)
	}
	if num == "" {
		num = "0"
	}

	var val interface{}
	if strings.ContainsAny(num, "eE+-.") {
		const prec = 512 // matches current implementation of go/constant

		// big.ParseFloat supports all cases we care about. Extra features
		// like hexadecimal float or number_spacing are disabled by filtering
		// with AcceptUntil above.
		val, _, err = big.ParseFloat(num, 10, prec, big.ToNearestEven)
	} else {
		var ok bool
		val, ok = new(big.Int).SetString(num, 10)
		if !ok {
			err = fmt.Errorf("parsing '%v': invalid integer", num)
		}
	}
	if err != nil {
		return l.Error(err)
	}
	l.Emit(TokenNumber, constant.Make(val))
	return state.lex
}
