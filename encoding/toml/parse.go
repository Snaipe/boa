// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package toml

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	. "snai.pe/boa/syntax"
)

type parser struct {
	lexer *Lexer
	name  string
	prev  *Token
}

func newParser(name string, in io.Reader) *parser {
	p := parser{
		lexer: newLexer(in),
		name:  name,
	}
	return &p
}

func (p *parser) Next(tokens *[]Token, allowed ...TokenType) (token Token, err error) {
outer:
	for {
		if p.prev != nil {
			token, p.prev = *p.prev, nil
		} else {
			token = p.lexer.Next()
		}
		if token.Type == TokenError {
			return token, p.Error(token, nil)
		}
		for _, typ := range allowed {
			if token.Type == typ {
				*tokens = append(*tokens, token)
				continue outer
			}
		}
		return token, nil
	}
}

func digest(paths ...[]interface{}) string {
	var pathstr strings.Builder
	for _, path := range paths {
		for i := range path {
			switch v := path[i].(type) {
			case string:
				io.WriteString(&pathstr, v)
				pathstr.WriteByte(0)
			case int:
				binary.Write(&pathstr, binary.BigEndian, int64(v))
			default:
				panic(fmt.Sprintf("unknown keypath component %T", v))
			}
		}
	}
	return pathstr.String()
}

func pretty(paths ...[]interface{}) string {
	var pathstr strings.Builder
	for _, path := range paths {
		for i := range path {
			switch v := path[i].(type) {
			case string:
				pathstr.WriteString(v)
			case int:
				fmt.Fprintf(&pathstr, "[%d]", v)
			}
			pathstr.WriteByte('.')
		}
	}
	out := pathstr.String()
	if len(out) > 0 {
		out = out[:len(out)-1]
	}
	return out
}

type DuplicateKeyError struct {
	Kind     string
	Prefix   []interface{}
	Path     []interface{}
	Position Cursor
}

func (e *DuplicateKeyError) Error() string {
	return fmt.Sprintf("%s %s is already defined at line %d, column %d", e.Kind, pretty(e.Prefix, e.Path), e.Position.Line, e.Position.Column)
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
	err = TokenTypeError{Token: token, Err: err}
	err = Error{Filename: p.name, Cursor: token.Start, Err: err}
	return err
}

func (p *parser) Parse() (*Node, error) {
	var document Node
	document.Type = NodeDocument

	type Table struct {
		at       Cursor
		idx      int
		array    bool
		inline   bool
		explicit bool
		kind     string
	}

	rootval := Node{
		Type: NodeMap,
	}

	knownkeys := make(map[string]Table)
	knownkeyslocal := make(map[string]Table)

	var currentPath []interface{}

	rootprev := &rootval.Child
	localprev := &rootval.Child
	prev := &rootprev

out:
	for {
		key := &Node{}
		token, err := p.Next(&key.Tokens, TokenWhitespace, TokenNewline, TokenComment)
		if err != nil {
			return nil, err
		}

		delim := TokenEqual
		switch toktype := token.Type; toktype {
		case TokenLSquare, TokenDoubleLSquare:
			key.Tokens = append(key.Tokens, token)
			token, err = p.Next(&key.Tokens, TokenWhitespace)
			if err != nil {
				return nil, err
			}
			delim = TokenRSquare
			if toktype == TokenDoubleLSquare {
				delim = TokenDoubleRSquare
			}
		case TokenEOF:
			break out
		}

		err = p.Key(token, key, delim)
		if err != nil {
			return nil, err
		}
		key.Position = token.Start

		kpath := key.Value.([]interface{})
		path := make([]interface{}, 0, len(kpath)*2+len(currentPath))

		suffix := &key.Suffix

		switch delim {
		case TokenEqual:
			// <key> = <value>

			for i := 0; i < len(kpath)-1; i++ {
				path = append(path, kpath[i])

				keydigest := digest(currentPath, path)
				tbl := knownkeys[keydigest]
				if tbl.explicit || tbl.inline {
					return nil, p.Error(token, &DuplicateKeyError{Kind: tbl.kind, Prefix: currentPath, Path: path, Position: tbl.at})
				}
				tbl = knownkeyslocal[keydigest]
				tbl.at = key.Position
				tbl.explicit = true
				tbl.kind = "key"
				knownkeyslocal[keydigest] = tbl
			}

			path = append(path, kpath[len(kpath)-1])

			keydigest := digest(currentPath, path)
			tbl := knownkeys[keydigest]
			if tbl.explicit {
				return nil, p.Error(token, &DuplicateKeyError{Kind: tbl.kind, Prefix: currentPath, Path: path, Position: tbl.at})
			}
			tbl = knownkeyslocal[keydigest]
			if tbl.explicit {
				return nil, p.Error(token, &DuplicateKeyError{Kind: tbl.kind, Prefix: currentPath, Path: path, Position: tbl.at})
			}
			tbl.at = key.Position
			tbl.explicit = true
			tbl.inline = true
			tbl.kind = "key"
			knownkeyslocal[keydigest] = tbl

			key.Value = path

			value := &Node{}
			token, err = p.Next(&value.Tokens, TokenWhitespace)
			if err != nil {
				return nil, err
			}
			if err := p.Value(token, value); err != nil {
				return nil, err
			}
			key.Child = value
			suffix = &value.Suffix

			**prev = key
			*prev = &key.Sibling

		case TokenRSquare, TokenDoubleRSquare:
			// [<key>] or [[<key>]]

			if len(knownkeyslocal) > 0 {
				for k, v := range knownkeyslocal {
					knownkeys[k] = v
				}
				knownkeyslocal = make(map[string]Table)
			}

			// check that none of the newly defined keys in the path conflict
			// with other top-level definitions, or redefine explicit keys

			for i := 0; i < len(kpath)-1; i++ {
				path = append(path, kpath[i])

				keydigest := digest(path)
				tbl, ok := knownkeys[keydigest]
				if !ok {
					tbl.at = key.Position
					tbl.kind = "table"
					knownkeys[keydigest] = tbl
				} else if tbl.inline {
					return nil, p.Error(token, &DuplicateKeyError{Kind: tbl.kind, Path: path, Position: tbl.at})
				}

				if tbl.array {
					path = append(path, tbl.idx)
				}
			}
			path = append(path, kpath[len(kpath)-1])

			keydigest := digest(path)
			tbl, ok := knownkeys[keydigest]

			switch delim {
			case TokenRSquare:
				// [<key>]

				if tbl.explicit {
					return nil, p.Error(token, &DuplicateKeyError{Kind: tbl.kind, Path: path, Position: tbl.at})
				}
				tbl.kind = "table"

			case TokenDoubleRSquare:
				// [[<key>]]

				if ok && !tbl.array {
					return nil, p.Error(token, &DuplicateKeyError{Kind: tbl.kind, Path: path, Position: tbl.at})
				}

				if !tbl.explicit {
					tbl.array = true
					tbl.kind = "array"
				} else {
					tbl.idx++
				}
				path = append(path, tbl.idx)

			}

			tbl.at = key.Position
			tbl.explicit = true
			knownkeys[keydigest] = tbl
			key.Value = path

			prev = &rootprev
			**prev = key
			*prev = &key.Sibling

			table := &Node{Type: NodeMap}
			key.Child = table
			localprev = &table.Child
			currentPath = path
			prev = &localprev

		default:
			panic("unknown top-level key definition")
		}

		token, err = p.Next(suffix, TokenWhitespace, TokenComment)
		if err != nil {
			return nil, err
		}
		if token.Type == TokenEOF {
			break
		}
		if token.Type != TokenNewline {
			return nil, p.Error(token, UnexpectedTokenError{TokenNewline, TokenEOF})
		}
		key.Suffix = append(key.Suffix, token)
	}

	document.Child = &rootval
	return &document, nil
}

func (p *parser) Key(token Token, key *Node, delim TokenType) error {
	key.Type = NodeKeyPath

	var path []interface{}
	for {
		switch token.Type {
		case TokenIdentifier, TokenString:
			key.Tokens = append(key.Tokens, token)
			path = append(path, token.Value.(string))

		// Need to handle these as well since `true`, `false`, `1234`, `1.2`... are all
		// contextually identifiers
		case TokenNumber, TokenBool:
			key.Tokens = append(key.Tokens, token)
			for _, k := range strings.Split(token.Raw, ".") {
				path = append(path, k)
			}
		default:
			return p.Error(token, UnexpectedTokenError{TokenIdentifier, TokenString})
		}

		if strings.ContainsAny(token.Raw, "\r\n") {
			return p.Error(token, fmt.Errorf("keys cannot be multiline strings"))
		}

		var err error
		token, err = p.Next(&key.Tokens, TokenWhitespace)
		if err != nil {
			return err
		}
		if token.Type == delim {
			break
		}
		if token.Type != TokenDot {
			return p.Error(token, UnexpectedTokenError{TokenDot, delim})
		}
		token, err = p.Next(&key.Tokens, TokenWhitespace)
		if err != nil {
			return err
		}
	}
	key.Suffix = append(key.Suffix, token)
	key.Value = path
	return nil
}

func (p *parser) Value(token Token, node *Node) error {
	switch token.Type {
	case TokenLBrace:
		if err := p.Object(token, node); err != nil {
			return err
		}
	case TokenDoubleLSquare:
		// This pretty funny case happens in situations like a = [[]]
		// and we have to split the token in two.
		token.Type = TokenLSquare
		token.Raw = "["
		next := token

		token.End.Column--
		next.Start.Column++
		p.prev = &next
		fallthrough
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
	case TokenDateTime:
		node.Type = NodeDateTime
		node.Value = token.Value
		node.Tokens = append(node.Tokens, token)
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

	allowed := []TokenType{TokenWhitespace, TokenComment}
	seen := make(map[string]Cursor)

	var err error
	token, err = p.Next(&node.Tokens, allowed...)
	if err != nil {
		return err
	}

	key := new(Node)
	prev := &node.Child
	last := node
	for token.Type != TokenRBrace {
		err := p.Key(token, key, TokenEqual)
		if err != nil {
			return err
		}
		key.Position = token.Start

		path := key.Value.([]interface{})
		keydigest := digest(path)
		if pos, dup := seen[keydigest]; dup {
			return p.Error(token, &DuplicateKeyError{Kind: "key", Path: path, Position: pos})
		}
		seen[keydigest] = key.Position

		*prev = key
		prev = &key.Sibling

		value := &Node{}
		key.Child = value

		token, err = p.Next(&value.Tokens, allowed...)
		if err != nil {
			return err
		}
		if err := p.Value(token, value); err != nil {
			return err
		}
		value.Position = token.Start

		token, err = p.Next(&value.Suffix, allowed...)
		if err != nil {
			return err
		}
		switch token.Type {
		// A key-value pair is always terminated by , or }.
		case TokenComma:
			key = new(Node)
			token, err = p.Next(&key.Tokens, allowed...)
			if err != nil {
				return err
			}
			if token.Type == TokenRBrace {
				return p.Error(token, UnexpectedTokenError{TokenIdentifier})
			}
		case TokenRBrace:
			// nothing to do, we'll break out of the loop
		default:
			return p.Error(token, UnexpectedTokenError{TokenComma, TokenRBrace})
		}
		last = value
	}
	last.Suffix = append(last.Suffix, token)
	return nil
}

func (p *parser) List(token Token, node *Node) error {
	node.Type = NodeList
	node.Tokens = append(node.Tokens, token)

	allowed := []TokenType{TokenWhitespace, TokenNewline, TokenComment}

	fixup := func(token *Token) {
		if token.Type != TokenDoubleRSquare {
			return
		}

		// We also have to fixup double square brackets here
		token.Type = TokenRSquare
		token.Raw = "]"
		next := *token

		token.End.Column--
		next.Start.Column++
		p.prev = &next
	}

	var err error
	token, err = p.Next(&node.Tokens, allowed...)
	if err != nil {
		return err
	}
	fixup(&token)
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

		token, err = p.Next(&entry.Suffix, allowed...)
		if err != nil {
			return err
		}
		fixup(&token)
		if token.Type == TokenComma {
			entry.Suffix = append(entry.Suffix, token)
			token, err = p.Next(&entry.Suffix, allowed...)
			if err != nil {
				return err
			}
		} else if token.Type != TokenRSquare {
			return p.Error(token, UnexpectedTokenError{TokenRSquare})
		}
		last = entry
	}
	last.Suffix = append(last.Suffix, token)
	return nil
}
