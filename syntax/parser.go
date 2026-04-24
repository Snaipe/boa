// Copyright 2026 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax

import "fmt"

// ParserBase holds the fields and methods common to all format parsers:
// the underlying Lexer, a token pushback stack, and error propagation.
// Embed it in format-specific parser structs.
type ParserBase struct {
	Lexer *Lexer
	prev  []Token
}

// RawNext returns the next token, draining the pushback stack first.
func (p *ParserBase) RawNext() Token {
	if len(p.prev) > 0 {
		last := len(p.prev) - 1
		tok := p.prev[last]
		p.prev = p.prev[:last]
		return tok
	}
	return p.Lexer.Next()
}

// Back pushes tokens back onto the stack in original order.
func (p *ParserBase) Back(toks ...Token) {
	for i := len(toks) - 1; i >= 0; i-- {
		p.prev = append(p.prev, toks[i])
	}
}

// Skip reads and collects tokens of the listed types, then returns the
// first token that does not match. If collect is non-nil, skipped tokens
// are appended to it. TokenError tokens cause an immediate Fail.
func (p *ParserBase) Skip(collect *[]Token, allowed ...TokenType) Token {
	for {
		tok := p.RawNext()
		if tok.Type == TokenError {
			p.Fail(tok, nil)
		}
		if !tok.IsAny(allowed...) {
			return tok
		}
		if collect != nil {
			*collect = append(*collect, tok)
		}
	}
}

// Fail panics with a parse error rooted at tok's position.
// If tok carries TokenError its wrapped error is re-panicked directly.
// If err is nil a generic "unexpected <type>" message is used.
// The error is wrapped in TokenTypeError so the raw token text appears
// in the message.
func (p *ParserBase) Fail(tok Token, err error) {
	if tok.Type == TokenError {
		panic(tok.Value.(error))
	}
	if err == nil {
		err = fmt.Errorf("unexpected %v", tok.Type)
	}
	panic(&Error{Cursor: tok.Start, Err: TokenTypeError{Token: tok, Err: err}})
}

// Recover is meant to be deferred at the top of Parse methods. It converts
// panics from Fail back into error return values.
func Recover(err *error) {
	if r := recover(); r != nil {
		if e, ok := r.(*Error); ok {
			*err = e
		} else if e, ok := r.(error); ok {
			*err = e
		} else {
			panic(r)
		}
	}
}
