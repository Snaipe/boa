// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// Cursor represents a cursor position within a document, i.e. a line and
// a column number, both starting at 1.
type Cursor struct {
	Line, Column int
}

type Token struct {
	// The type of this token.
	Type TokenType

	// The original string representation of this token.
	Raw string

	// The value interpreted from Raw (may be nil).
	Value interface{}

	// The starting position of this token.
	Start Cursor

	// The end position of this token.
	End Cursor
}

type TokenType string

const (
	TokenEOF           TokenType = ""
	TokenError         TokenType = "<error>"
	TokenComment       TokenType = "<comment>"
	TokenInlineComment TokenType = "<inline-comment>"
	TokenString        TokenType = "<string>"
	TokenNumber        TokenType = "<number>"
	TokenBool          TokenType = "<bool>"
	TokenIdentifier    TokenType = "<identifier>"
	TokenNil           TokenType = "<nil>"
	TokenNewline       TokenType = "<newline>"
	TokenWhitespace    TokenType = "<whitespace>"
)

func (typ TokenType) String() string {
	if typ == TokenEOF {
		return "<eof>"
	}
	return string(typ)
}

type backbuffer struct {
	buf [2]struct{
		r   rune
		w   int
		pos Cursor
	}
	ridx int
	rlen int
	widx int
}

func (b *backbuffer) inc(i, inc int) int {
	i = (i + inc) % len(b.buf)
	if i < 0 {
		i += len(b.buf)
	}
	return i
}

func (b *backbuffer) cap() int {
	return cap(b.buf)
}

func (b *backbuffer) write(r rune, w int, pos Cursor) {
	if b.rlen != 0 {
		panic("programming error: can't write into backbuffer while there are unread runes")
	}
	e := &b.buf[b.widx]
	e.r, e.w, e.pos = r, w, pos
	b.widx = b.inc(b.widx, 1)
}

func (b *backbuffer) read() (rune, int, Cursor) {
	if b.rlen == 0 {
		panic("programming error: no runes in backbuffer")
	}
	e := &b.buf[b.inc(b.widx, -b.rlen)]
	b.rlen--
	return e.r, e.w, e.pos
}

func (b *backbuffer) unread() (rune, int, Cursor) {
	if b.rlen >= len(b.buf) {
		panic("programming error: can't unread more bytes than backbuffer capacity")
	}
	b.rlen++
	ret := &b.buf[b.inc(b.widx, -b.rlen)]
	if ret.w == 0 {
		panic("programming error: can't unread more bytes than backbuffer length")
	}
	return ret.r, ret.w, ret.pos
}

type StateFunc func(*Lexer) StateFunc

type Lexer struct {
	// The input of this lexer. Typically a bufio.Reader.
	Input io.RuneReader

	// The cursor position marking the start of the current token.
	TokenPosition Cursor

	// The current cursor position at which the lexer is reading.
	Position Cursor

	init    StateFunc    // initial state
	state   StateFunc    // current state
	token   bytes.Buffer // current token
	tokens  chan Token   // token ring buffer
	prev    backbuffer   // stashed runes for UnreadRune
	unread  int          // number of unread bytes
}

func NewLexer(input io.Reader, init StateFunc) *Lexer {

	rscan, ok := input.(io.RuneReader)
	if !ok {
		rscan = bufio.NewReader(input)
	}

	l := Lexer{
		Input: rscan,
		init:  init,
	}
	l.Reset()
	return &l
}

func (l *Lexer) Reset() {
	l.state = l.init
	l.Position = Cursor{1, 1}
	l.TokenPosition = l.Position
	l.tokens = make(chan Token, 2)
	l.token.Reset()
}

func (l *Lexer) Next() Token {
	for {
		select {
		case token := <-l.tokens:
			return token
		default:
			l.state = l.state(l)
		}
	}
}

func (l *Lexer) Error(err error) StateFunc {
	typ := TokenError
	if err == io.EOF {
		typ = TokenEOF
	}
	if _, ok := err.(Error); !ok {
		err = Error{Cursor: l.TokenPosition, Err: err}
	}
	token := Token{
		Type:  typ,
		Value: err,
		Start: l.TokenPosition,
		End:   l.Position,
	}
	l.tokens <- token
	l.TokenPosition = l.Position
	close(l.tokens)
	return nil
}

func (l *Lexer) Errorf(format string, args ...interface{}) StateFunc {
	return l.Error(fmt.Errorf(format, args...))
}

func (l *Lexer) Emit(typ TokenType, val interface{}) {
	token := Token{
		Type:  typ,
		Raw:   l.Token(),
		Value: val,
		Start: l.TokenPosition,
		End:   l.Position,
	}
	l.token.Reset()
	l.tokens <- token
	l.TokenPosition = l.Position
}

func (l *Lexer) Discard() {
	l.token.Reset()
	l.TokenPosition = l.Position
}

func (l *Lexer) ReadRune() (r1 rune, w1 int, err1 error) {
	if l.unread > 0 {
		r, w, pos := l.prev.read()
		l.Position = pos
		l.unread--
		l.token.WriteRune(r)
		return r, w, nil
	}

	r, w, err := l.Input.ReadRune()
	if err != nil {
		return 0, 0, err
	}
	l.token.WriteRune(r)
	l.prev.write(r, w, l.Position)
	switch r {
	case '\n':
		l.Position.Line++
		l.Position.Column = 0
	default:
		l.Position.Column++
	}
	return r, w, nil
}

func (l *Lexer) UnreadRune() error {
	_, w, _ := l.prev.unread()
	l.unread++
	l.token.Truncate(l.token.Len()-w)
	return nil
}

func (l *Lexer) PeekRune() (rune, int, error) {
	r, w, err := l.ReadRune()
	if err != nil {
		return 0, 0, err
	}
	l.UnreadRune()
	return r, w, nil
}

func (l *Lexer) Token() string {
	return string(l.token.Bytes())
}

func (l *Lexer) AcceptRune(exp rune) (rune, error) {
	r, _, err := l.ReadRune()
	switch {
	case err != nil:
		return 0, err
	case r != exp:
		return r, fmt.Errorf("expected character %q, got %q", r, exp)
	}
	return r, nil
}

func (l *Lexer) AcceptString(exp string) (string, error) {
	for _, rexp := range exp {
		r, _, err := l.ReadRune()
		switch {
		case err != nil:
			return "", err
		case r != rexp:
			return "", fmt.Errorf("unexpected character %q in expected string %q", r, exp)
		}
	}
	return exp, nil
}

func (l *Lexer) AcceptFunc(fn func(rune) bool) (rune, error) {
	r, _, err := l.ReadRune()
	switch {
	case err != nil:
		return 0, err
	case !fn(r):
		return r, fmt.Errorf("unexpected character %q", r)
	}
	return r, nil
}

func (l *Lexer) AcceptUntil(fn func(rune) bool) (string, error) {
	var out strings.Builder
	for {
		r, _, err := l.ReadRune()
		if err != nil {
			return "", err
		}
		if !fn(r) {
			break
		}
		out.WriteRune(r)
	}
	l.UnreadRune()
	return out.String(), nil
}
