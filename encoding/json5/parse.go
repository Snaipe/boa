// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package json5

import (
	"go/constant"
	gotokens "go/token"
	"io"

	. "snai.pe/boa/syntax"
)

type parser struct {
	lexer   *Lexer
	name    string
	current *Node
}

func newParser(name string, in io.Reader) *parser {
	p := parser{
		lexer: newLexer(in),
		name:  name,
	}
	return &p
}

func (p *parser) Next(tokens *[]Token) (token Token) {
	for {
		token = p.lexer.Next()
		switch token.Type {
		case TokenComment, TokenInlineComment, TokenNewline, TokenWhitespace:
			*tokens = append(*tokens, token)
		default:
			return token
		}
	}
}

func (p *parser) Error(token Token, err error) error {
	if token.Type == TokenError {
		err = token.Value.(error)
		if e, ok := err.(Error); ok {
			e.Filename = p.name
			err = e
		}
		return err
	}
	err = TokenTypeError{Type: token.Type, Err: err}
	err = Error{Filename: p.name, Cursor: token.Start, Err: err}
	return err
}

func (p *parser) Parse() (*Node, error) {
	var node Node
	node.Type = NodeDocument

	var rootval Node
	node.Child = &rootval

	token := p.Next(&rootval.Tokens)
	if token.Type == TokenError {
		return nil, p.Error(token, nil)
	}
	if err := p.Value(token, &rootval); err != nil {
		return nil, err
	}
	rootval.Position = token.Start
	token = p.Next(&rootval.Suffix)
	if token.Type != TokenEOF {
		return nil, p.Error(token, UnexpectedTokenError{TokenEOF})
	}

	return &node, nil
}

func (p *parser) Value(token Token, node *Node) error {
	switch token.Type {
	case TokenLBrace:
		if err := p.Object(token, node); err != nil {
			return err
		}
	case TokenLSquare:
		if err := p.List(token, node); err != nil {
			return err
		}
	case TokenString:
		node.Type = NodeString
		node.Value = token.Value
		node.Tokens = append(node.Tokens, token)
	case TokenNumber:
		node.Type = NodeNumber
		node.Value = token.Value
		node.Tokens = append(node.Tokens, token)
	case TokenBool:
		node.Type = NodeBool
		node.Value = token.Value
		node.Tokens = append(node.Tokens, token)
	case TokenNil:
		node.Type = NodeNil
		node.Tokens = append(node.Tokens, token)
	case TokenMinus, TokenPlus:
		node.Type = NodeNumber
		node.Tokens = append(node.Tokens, token)
		next := p.Next(&node.Tokens)
		switch next.Type {
		case TokenError:
			return p.Error(next, nil)
		case TokenNumber:
			break
		default:
			return p.Error(next, ErrUnexpectedToken)
		}
		if token.Type == TokenMinus {
			node.Value = constant.UnaryOp(gotokens.SUB, next.Value.(constant.Value), 0)
		} else {
			node.Value = next.Value
		}
		node.Tokens = append(node.Tokens, next)
	case TokenError:
		return p.Error(token, nil)
	default:
		return p.Error(token, ErrUnexpectedToken)
	}

	return nil
}

func (p *parser) Object(token Token, node *Node) error {
	node.Type = NodeMap
	node.Tokens = append(node.Tokens, token)

	token = p.Next(&node.Tokens)
	prev := &node.Child
	last := node
	for token.Type != TokenRBrace {
		key := new(Node)

		switch token.Type {
		case TokenIdentifier, TokenString:
			key.Value = token.Value
		default:
			return p.Error(token, UnexpectedTokenError{TokenString, TokenIdentifier})
		}
		key.Type = NodeString
		key.Tokens = append(key.Tokens, token)
		key.Position = token.Start
		*prev = key
		prev = &key.Sibling

		token = p.Next(&key.Tokens)
		if token.Type != TokenColon {
			return p.Error(token, UnexpectedTokenError{TokenColon})
		}
		key.Tokens = append(key.Tokens, token)

		value := &Node{}
		key.Child = value

		token = p.Next(&value.Tokens)
		if err := p.Value(token, value); err != nil {
			return err
		}
		value.Position = token.Start

		token = p.Next(&value.Suffix)
		if token.Type == TokenComma {
			value.Suffix = append(value.Suffix, token)
			token = p.Next(&value.Suffix)
		} else if token.Type != TokenRBrace {
			return p.Error(token, UnexpectedTokenError{TokenRBrace})
		}
		last = value
	}
	last.Suffix = append(last.Suffix, token)
	return nil
}

func (p *parser) List(token Token, node *Node) error {
	node.Type = NodeList
	node.Tokens = append(node.Tokens, token)

	token = p.Next(&node.Tokens)
	last := node
	prev := &node.Child
	for token.Type != TokenRSquare {
		entry := &Node{}
		if err := p.Value(token, entry); err != nil {
			return err
		}
		entry.Position = token.Start
		*prev = entry
		prev = &entry.Sibling

		token = p.Next(&entry.Suffix)
		if token.Type == TokenComma {
			entry.Suffix = append(entry.Suffix, token)
			token = p.Next(&entry.Suffix)
		} else if token.Type != TokenRSquare {
			return p.Error(token, UnexpectedTokenError{TokenRSquare})
		}
		last = entry
	}
	last.Suffix = append(last.Suffix, token)
	return nil
}
