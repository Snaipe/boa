// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"unicode/utf8"
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

// IsAny returns true if the token is one of the specified token types.
func (tok *Token) IsAny(types ...TokenType) bool {
	for _, typ := range types {
		if tok.Type == typ {
			return true
		}
	}
	return false
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
	buf [4]struct {
		r    rune
		w    int
		next Cursor
		pos  Cursor
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

func (b *backbuffer) write(r rune, w int, next, pos Cursor) {
	if b.rlen != 0 {
		panic("programming error: can't write into backbuffer while there are unread runes")
	}
	e := &b.buf[b.widx]
	e.r, e.w, e.next, e.pos = r, w, next, pos
	b.widx = b.inc(b.widx, 1)
}

func (b *backbuffer) read() (rune, int, Cursor, Cursor) {
	if b.rlen == 0 {
		panic("programming error: no runes in backbuffer")
	}
	e := &b.buf[b.inc(b.widx, -b.rlen)]
	b.rlen--
	return e.r, e.w, e.next, e.pos
}

func (b *backbuffer) unread() (rune, int, Cursor, Cursor) {
	if b.rlen >= len(b.buf) {
		panic("programming error: can't unread more bytes than backbuffer capacity")
	}
	b.rlen++
	ret := &b.buf[b.inc(b.widx, -b.rlen)]
	if ret.w == 0 {
		panic("programming error: can't unread more bytes than backbuffer length")
	}
	return ret.r, ret.w, ret.next, ret.pos
}

type StateFunc func(*Lexer) StateFunc

// tokenQueue is a fixed-capacity ring buffer holding up to 2 tokens between
// Emit/Error calls and the next Next() call. Once close() is called the last
// token becomes sticky: next() keeps returning it without consuming it, so
// every subsequent Next() call sees the same EOF/error token.
type tokenQueue struct {
	buf    [2]Token
	head   int
	len    int
	closed bool
}

func (q *tokenQueue) push(tok Token) {
	if q.len >= len(q.buf) {
		panic("syntax: lexer token queue overflow")
	}
	q.buf[(q.head+q.len)%len(q.buf)] = tok
	q.len++
}

func (q *tokenQueue) close(tok Token) {
	q.push(tok)
	q.closed = true
}

// next returns the next queued token. Once closed, the last token is returned
// on every call without being consumed. Returns (Token{}, false) if empty.
func (q *tokenQueue) next() (Token, bool) {
	if q.len == 0 {
		return Token{}, false
	}
	tok := q.buf[q.head]
	// A state may emit a value token then immediately call Error (e.g. on EOF
	// mid-token), leaving more than one token queued with closed == true.
	// Drain normally until the last token, then keep it sticky.
	if !q.closed || q.len > 1 {
		q.head = (q.head + 1) % len(q.buf)
		q.len--
	}
	return tok, true
}

func (q *tokenQueue) reset() {
	*q = tokenQueue{}
}

type Lexer struct {
	// Input is the buffered I/O source for this lexer. State functions
	// read from it indirectly via ReadRune/UnreadRune; call Input.Reset
	// to switch the underlying reader without allocating.
	Input BufRuneReader

	// The cursor position marking the start of the current token.
	TokenPosition Cursor

	// The cursor position at which the lexer will be reading next.
	NextPosition Cursor

	// The cursor position of the current rune.
	Position Cursor

	// Context is checked at the start of each token in Next().
	// When cancelled, Next() returns a TokenError carrying ctx.Err().
	// Must not be nil.
	Context context.Context

	init   StateFunc    // initial state
	state  StateFunc    // current state
	token  bytes.Buffer // current token
	queue  tokenQueue   // pending tokens between Emit/Error and Next
	prev   backbuffer   // stashed runes for UnreadRune
	unread int          // number of unread bytes
	// Done is called once when the lexer terminates (after the EOF or error
	// token is sent). Use it to return per-parse resources to a sync.Pool.
	Done func()

	// TODO: consider unifying pushback with prev (backbuffer) — they serve
	// different access patterns today but overlap in purpose.
	pushback []byte // bytes to drain before reading from Input
	pboff    int    // read offset into pushback
}

func NewLexer(ctx context.Context, input io.Reader, init StateFunc) *Lexer {
	l := &Lexer{init: init}
	l.Context = ctx
	l.Input.Reset(input)
	l.Reset()
	return l
}

func (l *Lexer) Reset() {
	l.state = l.init
	l.NextPosition = Cursor{1, 1}
	l.Position = l.NextPosition
	l.TokenPosition = l.NextPosition
	l.queue.reset()
	l.token.Reset()
	l.prev = backbuffer{}
	l.unread = 0
	l.pushback = l.pushback[:0]
	l.pboff = 0
}

// Reinit prepares l for a new parse without allocating. It is equivalent to
// NewLexer but reuses the existing Lexer struct. Set l.Done before calling
// Reinit if pool cleanup is needed.
func (l *Lexer) Reinit(ctx context.Context, input io.Reader, init StateFunc) {
	l.Input.Reset(input)
	l.Context = ctx
	l.init = init
	l.Reset()
}

func (l *Lexer) Next() (tok Token) {
	// Call Done once the terminal token is about to be returned. Doing it here
	// -- after l.state = nil has been assigned -- prevents the pool-reuse race
	// where another goroutine writes to l.state before this assignment
	// completes. The deferred form also handles panics in state functions.
	defer func() {
		if l.Done != nil && (tok.Type == TokenEOF || tok.Type == TokenError) {
			done := l.Done
			l.Done = nil
			done()
		}
	}()
	for {
		var ok bool
		if tok, ok = l.queue.next(); ok {
			return
		}
		if err := l.Context.Err(); err != nil {
			tok = Token{
				Type:  TokenError,
				Value: &Error{Cursor: l.Position, Err: err},
				Start: l.Position,
				End:   l.Position,
			}
			return
		}
		l.state = l.state(l)
	}
}

func (l *Lexer) Error(err error) StateFunc {
	typ := TokenError
	if err == io.EOF {
		typ = TokenEOF
		// Leave token.Value = io.EOF directly; parsers only dereference
		// token.Value inside the TokenError branch, never for TokenEOF.
	} else if _, ok := err.(*Error); !ok {
		err = &Error{Cursor: l.TokenPosition, Err: err}
	}
	token := Token{
		Type:  typ,
		Value: err,
		Raw:   l.Token(),
		Start: l.TokenPosition,
		End:   l.Position,
	}
	l.queue.close(token)
	l.TokenPosition = l.NextPosition
	return nil
}

func (l *Lexer) Errorf(format string, args ...interface{}) StateFunc {
	return l.Error(fmt.Errorf(format, args...))
}

func (l *Lexer) Emit(typ TokenType, val interface{}) {
	l.queue.push(Token{
		Type:  typ,
		Raw:   l.Token(),
		Value: val,
		Start: l.TokenPosition,
		End:   l.Position,
	})
	l.token.Reset()
	l.TokenPosition = l.NextPosition
}

func (l *Lexer) Discard() {
	l.token.Reset()
	l.TokenPosition = l.NextPosition
}

func (l *Lexer) ReadRune() (r rune, w int, err error) {
	if l.unread > 0 {
		r, w, l.NextPosition, l.Position = l.prev.read()
		l.unread--
	} else if l.pboff < len(l.pushback) {
		r, w = utf8.DecodeRune(l.pushback[l.pboff:])
		l.pboff += w
		if l.pboff >= len(l.pushback) {
			l.pushback = l.pushback[:0]
			l.pboff = 0
		}
		l.prev.write(r, w, l.NextPosition, l.Position)
	} else {
		r, w, err = l.Input.ReadRune()
		if err != nil {
			return 0, 0, err
		}
		if r == utf8.RuneError {
			return 0, 0, fmt.Errorf("bad UTF-8 character")
		}
		l.prev.write(r, w, l.NextPosition, l.Position)
	}
	l.token.WriteRune(r)
	l.Position = l.NextPosition
	switch r {
	case '\n':
		l.NextPosition.Line++
		l.NextPosition.Column = 1
	default:
		l.NextPosition.Column++
	}
	return r, w, nil
}

func (l *Lexer) UnreadRune() error {
	_, w, next, pos := l.prev.unread()
	l.unread++
	l.NextPosition = next
	l.Position = pos
	l.token.Truncate(l.token.Len() - w)
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

// PeekPrefix reads up to n runes ahead without consuming them and returns the
// resulting string. A short result (len < n) means EOF was reached. Non-EOF
// errors are returned. n must not exceed the backbuffer capacity (4).
func (l *Lexer) PeekPrefix(n int) (string, error) {
	var buf [4]rune
	var read int
	var retErr error
	for read < n {
		r, _, err := l.ReadRune()
		if err != nil {
			if err != io.EOF {
				retErr = err
			}
			break
		}
		buf[read] = r
		read++
	}
	for i := 0; i < read; i++ {
		l.UnreadRune()
	}
	return string(buf[:read]), retErr
}

func (l *Lexer) Token() string {
	return string(l.token.Bytes())
}

// TokenLen returns the byte length of the current token buffer without
// allocating a string copy.
func (l *Lexer) TokenLen() int {
	return l.token.Len()
}

// TokenBytes returns the raw bytes of the current token buffer without
// allocating a string copy. The slice is only valid until the next
// ReadRune or Accept* call.
func (l *Lexer) TokenBytes() []byte {
	return l.token.Bytes()
}

func (l *Lexer) AcceptRune(exp rune) (rune, error) {
	r, _, err := l.ReadRune()
	switch {
	case err != nil:
		return 0, err
	case r != exp:
		return r, fmt.Errorf("expected character %q, got %q", exp, r)
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

// AcceptWhile accepts runes as long as fn(r) returns true, stopping at EOF
// or at the first rune for which fn returns false (which is pushed back).
func (l *Lexer) AcceptWhile(fn func(rune) bool) (string, error) {
	var out strings.Builder
	for {
		r, _, err := l.ReadRune()
		if err == io.EOF {
			return out.String(), nil
		}
		if err != nil {
			return "", err
		}
		if !fn(r) {
			l.UnreadRune()
			return out.String(), nil
		}
		out.WriteRune(r)
	}
}

// AcceptRun accepts characters from the character set chars, returning the
// accepted string. It stops at EOF or at any character not in chars.
func (l *Lexer) AcceptRun(chars string) (string, error) {
	return l.AcceptWhile(func(r rune) bool {
		return strings.ContainsRune(chars, r)
	})
}

// RequireLF reads one rune and requires it to be '\n'. Used after reading '\r'
// to consume the mandatory LF half of a CRLF sequence.
func (l *Lexer) RequireLF() error {
	r, _, err := l.ReadRune()
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	if err != nil {
		return err
	}
	if r != '\n' {
		return fmt.Errorf("unexpected character %q: expected LF after CR", r)
	}
	return nil
}

// AcceptNewline completes consuming a newline sequence when r has already been
// read: if r is '\r', reads and requires the following '\n' (CRLF); if r is
// '\n', it is a no-op. Use this to replace the `case '\r': RequireLF();
// fallthrough; case '\n':` pattern with the cleaner `case '\r', '\n':`.
func (l *Lexer) AcceptNewline(r rune) error {
	if r == '\r' {
		return l.RequireLF()
	}
	return nil
}

// SkipOptionalLF consumes the next rune if it is '\n'. Used after reading '\r'
// when a following LF is not mandatory (e.g. inside quoted scalars).
func (l *Lexer) SkipOptionalLF() {
	if r, _, err := l.ReadRune(); err == nil && r != '\n' {
		l.UnreadRune()
	}
}

// TruncateToken trims the current token buffer to n bytes and restores the
// cursor positions to the values recorded at that length. Used by the
// concurrent NDK machines to backtrack without calling UnreadRune repeatedly.
func (l *Lexer) TruncateToken(n int, pos, next Cursor) {
	l.token.Truncate(n)
	l.Position = pos
	l.NextPosition = next
}

// PushBack injects s at the front of the input, to be read before any further
// bytes from the original source.
func (l *Lexer) PushBack(s string) {
	if len(s) == 0 {
		return
	}
	l.pushback = append(l.pushback[:0], s...)
	l.pboff = 0
}

// EmitRaw emits a pre-built token directly to the token stream.
func (l *Lexer) EmitRaw(tok Token) {
	l.token.Reset()
	l.queue.push(tok)
	l.TokenPosition = l.NextPosition
}

// ParseUnicodeEscape reads length hex digits and returns the corresponding rune.
func (l *Lexer) ParseUnicodeEscape(length int) (rune, error) {
	var codepoint rune
	for i := 0; i < length; i++ {
		r, err := l.AcceptFunc(func(r rune) bool {
			return strings.ContainsRune("0123456789abcdefABCDEF", r)
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
		default:
			digit = r - '0'
		}
		codepoint = codepoint | (digit << (4 * (length - 1 - i)))
	}
	return codepoint, nil
}
