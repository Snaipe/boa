// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package json5

import (
	"context"
	"fmt"
	"go/constant"
	gotokens "go/token"
	"io"

	. "snai.pe/boa/syntax"
)

type parser struct {
	lexer *Lexer
	prev  []Token
}

func newParser(ctx context.Context, in io.Reader) Parser {
	p := parser{
		lexer: newLexer(ctx, in),
	}
	return &p
}

func (p *parser) Next(tokens *[]Token) (token Token) {
	for {
		if len(p.prev) > 0 {
			last := len(p.prev) - 1
			token, p.prev = p.prev[last], p.prev[:last]
		} else {
			token = p.lexer.Next()
		}
		switch token.Type {
		case TokenComment, TokenInlineComment, TokenNewline, TokenWhitespace:
			if tokens != nil {
				*tokens = append(*tokens, token)
			}
		default:
			return token
		}
	}
}

func (p *parser) back(tokens ...Token) {
	for i := len(tokens) - 1; i >= 0; i-- {
		p.prev = append(p.prev, tokens[i])
	}
}

func (p *parser) fail(token Token, err error) {
	if token.Type == TokenError {
		panic(token.Value.(error))
	}
	err = TokenTypeError{Token: token, Err: err}
	panic(&Error{Cursor: token.Start, Err: err})
}

func (p *parser) accept(tokens *[]Token, expect ...TokenType) Token {
	tok := p.Next(tokens)
	for _, typ := range expect {
		if tok.Type == typ {
			if tokens != nil {
				*tokens = append(*tokens, tok)
			}
			return tok
		}
	}
	p.fail(tok, UnexpectedTokenError(expect))
	panic("unreachable")
}

func (p *parser) Parse() (doc *Document, err error) {
	defer func() {
		if e := recover(); e != nil {
			if ee, ok := e.(*Error); ok {
				err = ee
			} else if ee, ok := e.(error); ok {
				err = ee
			} else {
				panic(e)
			}
		}
	}()
	return p.document(), nil
}

func (p *parser) document() *Document {
	doc := &Document{}
	doc.Root = p.value()

	tok := p.Next(&doc.Root.Base().Suffix)
	if tok.Type != TokenEOF {
		p.fail(tok, UnexpectedTokenError{TokenEOF})
	}
	return doc
}

func (p *parser) value() Value {
	leading := make([]Token, 0, 4)
	token := p.Next(&leading)
	switch token.Type {
	case TokenLBrace:
		p.back(append(leading, token)...)
		return p.object()
	case TokenLSquare:
		p.back(append(leading, token)...)
		return p.list()
	case TokenString:
		return &String{Node: Node{Tokens: append(leading, token), Position: token.Start}, Value: token.Value.(string)}
	case TokenNumber:
		return &Number{Node: Node{Tokens: append(leading, token), Position: token.Start}, Value: token.Value}
	case TokenBool:
		return &Bool{Node: Node{Tokens: append(leading, token), Position: token.Start}, Value: token.Value.(bool)}
	case TokenNil:
		return &Nil{Node: Node{Tokens: append(leading, token), Position: token.Start}}
	case TokenMinus, TokenPlus:
		tokens := append(leading, token)
		next := p.Next(&tokens)
		if next.Type != TokenNumber {
			p.fail(next, UnexpectedTokenError{TokenNumber})
		}
		var val interface{}
		if token.Type == TokenMinus {
			switch constv := next.Value.(type) {
			case constant.Value:
				val = constant.UnaryOp(gotokens.SUB, constv, 0)
			case float64:
				val = -constv
			default:
				panic(fmt.Sprintf("unsupported type %T for number node", constv))
			}
		} else {
			val = next.Value
		}
		return &Number{Node: Node{Tokens: append(tokens, next), Position: token.Start}, Value: val}
	default:
		p.fail(token, ErrUnexpectedToken)
		panic("unreachable")
	}
}

func (p *parser) key() Value {
	leading := make([]Token, 0, 4)
	token := p.Next(&leading)
	switch token.Type {
	case TokenIdentifier, TokenString:
	default:
		p.fail(token, UnexpectedTokenError{TokenString, TokenIdentifier})
	}
	key := &String{
		Node: Node{
			Tokens:   append(leading, token),
			Position: token.Start,
		},
		Value: token.Value.(string),
	}
	p.accept(&key.Tokens, TokenColon)
	return key
}

func (p *parser) object() *Map {
	node := &Map{}
	open := p.accept(&node.Tokens, TokenLBrace)
	node.Position = open.Start

	token := p.Next(&node.Tokens)
	for token.Type != TokenRBrace {
		p.back(token)
		key := p.key()
		value := p.value()

		entry := &MapEntry{Key: key, Value: value}
		node.Entries = append(node.Entries, entry)

		token = p.Next(&value.Base().Suffix)
		if token.Type == TokenComma {
			value.Base().Suffix = append(value.Base().Suffix, token)
			token = p.Next(&value.Base().Suffix)
		} else if token.Type != TokenRBrace {
			p.fail(token, UnexpectedTokenError{TokenRBrace})
		}
	}

	// Closing brace goes into the last entry's value suffix, or the map's
	// own suffix if empty.
	if len(node.Entries) > 0 {
		last := node.Entries[len(node.Entries)-1]
		last.Value.Base().Suffix = append(last.Value.Base().Suffix, token)
	} else {
		node.Suffix = append(node.Suffix, token)
	}
	return node
}

func (p *parser) list() *List {
	node := &List{}
	open := p.accept(&node.Tokens, TokenLSquare)
	node.Position = open.Start

	token := p.Next(&node.Tokens)
	for token.Type != TokenRSquare {
		p.back(token)
		entry := p.value()
		node.Items = append(node.Items, entry)

		token = p.Next(&entry.Base().Suffix)
		if token.Type == TokenComma {
			entry.Base().Suffix = append(entry.Base().Suffix, token)
			token = p.Next(&entry.Base().Suffix)
		} else if token.Type != TokenRSquare {
			p.fail(token, UnexpectedTokenError{TokenRSquare})
		}
	}

	// Closing bracket goes into the last item's suffix, or the list's
	// own suffix if empty.
	if len(node.Items) > 0 {
		last := node.Items[len(node.Items)-1]
		last.Base().Suffix = append(last.Base().Suffix, token)
	} else {
		node.Suffix = append(node.Suffix, token)
	}
	return node
}
