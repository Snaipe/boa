// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package toml

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

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

func (p *parser) back(tokens ...Token) {
	for i := len(tokens) - 1; i >= 0; i-- {
		p.prev = append(p.prev, tokens[i])
	}
}

func (p *parser) Next(tokens *[]Token, allowed ...TokenType) Token {
outer:
	for {
		var token Token
		if len(p.prev) > 0 {
			last := len(p.prev) - 1
			token, p.prev = p.prev[last], p.prev[:last]
		} else {
			token = p.lexer.Next()
		}
		if token.Type == TokenError {
			p.fail(token, nil)
		}
		for _, typ := range allowed {
			if token.Type == typ {
				if tokens != nil {
					*tokens = append(*tokens, token)
				}
				continue outer
			}
		}
		return token
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

func (p *parser) fail(token Token, err error) {
	if token.Type == TokenError {
		panic(token.Value.(error))
	}
	err = TokenTypeError{Token: token, Err: err}
	panic(&Error{Cursor: token.Start, Err: err})
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
	type Table struct {
		at       Cursor
		idx      int
		array    bool
		inline   bool
		explicit bool
		kind     string
	}

	rootval := &Map{}

	knownkeys := make(map[string]Table)
	knownkeyslocal := make(map[string]Table)

	var currentPath []interface{}

	rootEntries := &rootval.Entries
	localEntries := &rootval.Entries
	prev := &rootEntries

	var trailingTokens []Token

out:
	for {
		keyTokens := make([]Token, 0, 4)
		token := p.Next(&keyTokens, TokenWhitespace, TokenNewline, TokenComment)

		delim := TokenEqual
		switch toktype := token.Type; toktype {
		case TokenLSquare, TokenDoubleLSquare:
			keyTokens = append(keyTokens, token)
			token = p.Next(&keyTokens, TokenWhitespace)
			delim = TokenRSquare
			if toktype == TokenDoubleLSquare {
				delim = TokenDoubleRSquare
			}
		case TokenEOF:
			rootval.Suffix = append(rootval.Suffix, keyTokens...)
			break out
		}

		p.back(token)
		key := p.Key(keyTokens, delim)

		kpath := key.Path
		path := make([]interface{}, 0, len(kpath)*2+len(currentPath))

		var suffix *[]Token

		switch delim {
		case TokenEqual:
			// <key> = <value>

			for i := 0; i < len(kpath)-1; i++ {
				path = append(path, kpath[i])

				keydigest := digest(currentPath, path)
				tbl := knownkeys[keydigest]
				if tbl.explicit || tbl.inline {
					p.fail(key.Base().Tokens[len(key.Base().Tokens)-1], &DuplicateKeyError{Kind: tbl.kind, Prefix: currentPath, Path: path, Position: tbl.at})
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
				p.fail(key.Base().Tokens[len(key.Base().Tokens)-1], &DuplicateKeyError{Kind: tbl.kind, Prefix: currentPath, Path: path, Position: tbl.at})
			}
			tbl = knownkeyslocal[keydigest]
			if tbl.explicit {
				p.fail(key.Base().Tokens[len(key.Base().Tokens)-1], &DuplicateKeyError{Kind: tbl.kind, Prefix: currentPath, Path: path, Position: tbl.at})
			}
			tbl.at = key.Position
			tbl.explicit = true
			tbl.inline = true
			tbl.kind = "key"
			knownkeyslocal[keydigest] = tbl

			key.Path = path

			value := p.Value()
			suffix = &value.Base().Suffix

			entry := &MapEntry{Key: key, Value: value}
			*(*prev) = append(*(*prev), entry)

		case TokenRSquare, TokenDoubleRSquare:
			// [<key>] or [[<key>]]

			if len(knownkeyslocal) > 0 {
				for k, v := range knownkeyslocal {
					knownkeys[k] = v
				}
				knownkeyslocal = make(map[string]Table)
			}

			for i := 0; i < len(kpath)-1; i++ {
				path = append(path, kpath[i])

				keydigest := digest(path)
				tbl, ok := knownkeys[keydigest]
				if !ok {
					tbl.at = key.Position
					tbl.kind = "table"
					knownkeys[keydigest] = tbl
				} else if tbl.inline {
					p.fail(key.Base().Tokens[len(key.Base().Tokens)-1], &DuplicateKeyError{Kind: tbl.kind, Path: path, Position: tbl.at})
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
				if tbl.explicit {
					p.fail(key.Base().Tokens[len(key.Base().Tokens)-1], &DuplicateKeyError{Kind: tbl.kind, Path: path, Position: tbl.at})
				}
				tbl.kind = "table"

			case TokenDoubleRSquare:
				if ok && !tbl.array {
					p.fail(key.Base().Tokens[len(key.Base().Tokens)-1], &DuplicateKeyError{Kind: tbl.kind, Path: path, Position: tbl.at})
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
			key.Path = path

			prev = &rootEntries
			table := &Map{}
			entry := &MapEntry{Key: key, Value: table}
			*(*prev) = append(*(*prev), entry)

			localEntries = &table.Entries
			currentPath = path
			prev = &localEntries

			// Read end-of-line for the section header
			eolTokens := make([]Token, 0, 4)
			eolToken := p.Next(&eolTokens, TokenWhitespace, TokenComment)
			if eolToken.Type == TokenEOF {
				trailingTokens = eolTokens
				break out
			}
			if eolToken.Type != TokenNewline {
				p.fail(eolToken, UnexpectedTokenError{TokenNewline, TokenEOF})
			}
			key.Suffix = append(key.Suffix, eolTokens...)
			key.Suffix = append(key.Suffix, eolToken)
			continue

		default:
			panic("unknown top-level key definition")
		}

		token = p.Next(suffix, TokenWhitespace, TokenComment)
		if token.Type == TokenEOF {
			trailingTokens = nil
			break
		}
		if token.Type != TokenNewline {
			p.fail(token, UnexpectedTokenError{TokenNewline, TokenEOF})
		}
		*suffix = append(*suffix, token)
	}

	if len(trailingTokens) > 0 {
		rootval.Suffix = append(rootval.Suffix, trailingTokens...)
	}

	return &Document{Root: rootval}
}

// Key parses a dotted key. leading contains any tokens already consumed before
// the first key component (e.g. leading whitespace, or the '[' of a section header).
// The caller must have backed off the first content token before calling Key.
func (p *parser) Key(leading []Token, delim TokenType) *KeyPath {
	key := &KeyPath{Node: Node{Tokens: leading}}
	token := p.Next(nil)
	key.Position = token.Start

	for {
		switch token.Type {
		case TokenIdentifier, TokenString:
			key.Tokens = append(key.Tokens, token)
			key.Path = append(key.Path, token.Value.(string))

		// Need to handle these as well since `true`, `false`, `1234`, `1.2`... are all
		// contextually identifiers
		case TokenNumber, TokenBool:
			key.Tokens = append(key.Tokens, token)
			for _, k := range strings.Split(token.Raw, ".") {
				key.Path = append(key.Path, k)
			}
		default:
			p.fail(token, UnexpectedTokenError{TokenIdentifier, TokenString})
		}

		if strings.ContainsAny(token.Raw, "\r\n") {
			p.fail(token, fmt.Errorf("keys cannot be multiline strings"))
		}

		token = p.Next(&key.Tokens, TokenWhitespace)
		if token.Type == delim {
			break
		}
		if token.Type != TokenDot {
			p.fail(token, UnexpectedTokenError{TokenDot, delim})
		}
		key.Tokens = append(key.Tokens, token)
		token = p.Next(&key.Tokens, TokenWhitespace)
	}
	key.Tokens = append(key.Tokens, token)
	return key
}

func (p *parser) Value() Value {
	leading := make([]Token, 0, 4)
	token := p.Next(&leading, TokenWhitespace)
	switch token.Type {
	case TokenLBrace:
		p.back(append(leading, token)...)
		return p.Object()
	case TokenDoubleLSquare:
		// This pretty funny case happens in situations like a = [[]]
		// and we have to split the token in two.
		lsquare := token
		lsquare.Type = TokenLSquare
		lsquare.Raw = "["
		next := lsquare
		lsquare.End.Column--
		next.Start.Column++
		p.back(next)
		p.back(append(leading, lsquare)...)
		return p.List()
	case TokenLSquare:
		p.back(append(leading, token)...)
		return p.List()
	case TokenString:
		return &String{Node: Node{Tokens: append(leading, token), Position: token.Start}, Value: token.Value.(string)}
	case TokenNumber:
		return &Number{Node: Node{Tokens: append(leading, token), Position: token.Start}, Value: token.Value}
	case TokenBool:
		return &Bool{Node: Node{Tokens: append(leading, token), Position: token.Start}, Value: token.Value.(bool)}
	case TokenDateTime:
		return &DateTime{Node: Node{Tokens: append(leading, token), Position: token.Start}, Value: token.Value}
	default:
		p.fail(token, ErrUnexpectedToken)
		panic("unreachable")
	}
}

func (p *parser) Object() *Map {
	node := &Map{}
	allowed := []TokenType{TokenWhitespace, TokenComment}
	seen := make(map[string]Cursor)

	open := p.Next(&node.Tokens, allowed...)
	if open.Type != TokenLBrace {
		p.fail(open, UnexpectedTokenError{TokenLBrace})
	}
	node.Tokens = append(node.Tokens, open)
	node.Position = open.Start

	token := p.Next(&node.Tokens, allowed...)
	for token.Type != TokenRBrace {
		p.back(token)
		key := p.Key(nil, TokenEqual)
		key.Position = key.Base().Tokens[0].Start

		keydigest := digest(key.Path)
		if pos, dup := seen[keydigest]; dup {
			p.fail(key.Base().Tokens[0], &DuplicateKeyError{Kind: "key", Path: key.Path, Position: pos})
		}
		seen[keydigest] = key.Position

		value := p.Value()
		value.Base().Position = value.Base().Tokens[0].Start

		entry := &MapEntry{Key: key, Value: value}
		node.Entries = append(node.Entries, entry)

		token = p.Next(&value.Base().Suffix, allowed...)
		switch token.Type {
		case TokenComma:
			value.Base().Suffix = append(value.Base().Suffix, token)
			token = p.Next(&value.Base().Suffix, allowed...)
			if token.Type == TokenRBrace {
				p.fail(token, UnexpectedTokenError{TokenIdentifier})
			}
		case TokenRBrace:
			// nothing to do, we'll break out of the loop
		default:
			p.fail(token, UnexpectedTokenError{TokenComma, TokenRBrace})
		}
	}

	node.Suffix = append(node.Suffix, token)
	return node
}

func (p *parser) List() *List {
	node := &List{}
	allowed := []TokenType{TokenWhitespace, TokenNewline, TokenComment}

	open := p.Next(&node.Tokens, allowed...)
	if open.Type != TokenLSquare {
		p.fail(open, UnexpectedTokenError{TokenLSquare})
	}
	node.Tokens = append(node.Tokens, open)
	node.Position = open.Start

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
		p.back(next)
	}

	token := p.Next(&node.Tokens, allowed...)
	fixup(&token)
	for token.Type != TokenRSquare {
		p.back(token)
		entry := p.Value()
		entry.Base().Position = entry.Base().Tokens[0].Start
		node.Items = append(node.Items, entry)

		token = p.Next(&entry.Base().Suffix, allowed...)
		fixup(&token)
		if token.Type == TokenComma {
			entry.Base().Suffix = append(entry.Base().Suffix, token)
			token = p.Next(&entry.Base().Suffix, allowed...)
			fixup(&token)
		} else if token.Type != TokenRSquare {
			p.fail(token, UnexpectedTokenError{TokenRSquare})
		}
	}

	// Closing bracket goes into last item suffix or list suffix if empty.
	if len(node.Items) > 0 {
		last := node.Items[len(node.Items)-1]
		last.Base().Suffix = append(last.Base().Suffix, token)
	} else {
		node.Suffix = append(node.Suffix, token)
	}
	return node
}
