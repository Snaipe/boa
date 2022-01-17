// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package json5

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"unicode/utf8"

	"snai.pe/boa/syntax"
	"snai.pe/boa/internal/reflectutil"
)

type Encoder struct {
	marshaler marshaler
}

func NewEncoder(wr io.Writer) *Encoder {
	encoder := Encoder{
		marshaler: marshaler{
			wr: wr,
		},
	}
	return &encoder
}

func (encoder *Encoder) Option(opts ...EncoderOption) *Encoder {
	for _, opt := range opts {
		opt(encoder)
	}
	return encoder
}

type runeWriter interface {
	Write([]byte) (int, error)
	WriteRune(r rune) (int, error)
}

type marshaler struct {
	// state
	wr      io.Writer
	depth   int
	newline bool

	// options
	json   bool
	indent string
	prefix string
}

func (m *marshaler) quote(in string, delim rune, json bool) (int, error) {
	written := 0
	for _, r := range in {
		switch r {
		case delim, '\n', '\r', parSep, lineSep:
			n, err := io.WriteString(m.wr, "\\")
			written += n
			if err != nil {
				return written, err
			}
			switch r {
			case '\n':
				if json {
					r = 'n'
				}
			case '\r':
				r = 'r'
			case lineSep:
				n, err := io.WriteString(m.wr, "u2028")
				if err != nil {
					return written, err
				}
				written += n
				continue
			case parSep:
				n, err := io.WriteString(m.wr, "u2029")
				if err != nil {
					return written, err
				}
				written += n
				continue
			}
		}
		var buf [utf8.UTFMax]byte
		l := utf8.EncodeRune(buf[:], r)

		n, err := m.wr.Write(buf[:l])
		written += n
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

func (m *marshaler) writeKey(s string) (int, error) {
	i := 0
	isNotIdChar := func(r rune) bool {
		ok := isIdentifierChar(r, i)
		i++
		return !ok
	}

	ident := !m.json && strings.IndexFunc(s, isNotIdChar) == -1
	if ident {
		return io.WriteString(m.wr, s)
	}

	written := 0
	n, err := io.WriteString(m.wr, "\"")
	written += n
	if err != nil {
		return written, err
	}

	n, err = m.quote(s, '"', false)
	written += n
	if err != nil {
		return written, err
	}

	n, err = io.WriteString(m.wr, "\"")
	written += n
	if err != nil {
		return written, err
	}
	return written, nil
}

func (m *marshaler) writeNewline() error {
	if _, err := io.WriteString(m.wr, "\n"); err != nil {
		return err
	}
	return m.writeIndent(m.indent, m.depth)
}

func (m *marshaler) writeIndent(indent string, level int) error {
	if indent == "" {
		indent = "  "
	}
	for i := 0; i < level; i++ {
		if _, err := io.WriteString(m.wr, indent); err != nil {
			return err
		}
	}
	return nil
}

func (m *marshaler) MarshalValue(v reflect.Value) (bool, error) {
	switch marshaler := v.Interface().(type) {
	case json.Marshaler:
		txt, err := marshaler.MarshalJSON()
		if err != nil {
			return false, err
		}

		node, err := newParser("", bytes.NewReader(txt)).Parse()
		if err != nil {
			return false, fmt.Errorf("json.Marshaler returned invalid JSON: %w", err)
		}

		if err := node.Marshal(m); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (m *marshaler) MarshalString(v reflect.Value) error {
	var err error
	switch s := v.Interface().(type) {
	case string:
		_, err = m.quote(s, '"', true)
	case []byte:
		_, err = m.quote(string(s), '"', true)
	default:
		err = fmt.Errorf("type %T cannot be marshaled into a string", s)
	}
	return err
}

func (m *marshaler) MarshalList(v reflect.Value) (bool, error) {
	m.depth++
	_, err := io.WriteString(m.wr, "[")
	return false, err
}

func (m *marshaler) MarshalListPost(v reflect.Value) error {
	m.depth--
	if v.Len() > 0 {
		if err := m.writeNewline(); err != nil {
			return err
		}
	}
	_, err := io.WriteString(m.wr, "]")
	return err
}

func (m *marshaler) MarshalListElem(l, v reflect.Value, i int) (bool, error) {
	return false, m.writeNewline()
}

func (m *marshaler) MarshalListElemPost(l, v reflect.Value, i int) error {
	if i != l.Len()-1 || !m.json {
		if _, err := io.WriteString(m.wr, ","); err != nil {
			return err
		}
	}
	return nil
}

func (m *marshaler) Stringify(v reflect.Value) (string, bool, error) {
	switch marshaler := v.Interface().(type) {
	case json.Marshaler:
		txt, err := marshaler.MarshalJSON()
		if err != nil {
			return "", false, err
		}

		node, err := newParser("", bytes.NewReader(txt)).Parse()
		if err != nil {
			return "", false, fmt.Errorf("json.Marshaler returned invalid JSON: %w", err)
		}
		if node.Child == nil || node.Child.Type != syntax.NodeString {
			return "", false, fmt.Errorf("unable to marshal object key: json.Marshaler returned a non-string value")
		}
		return node.Child.Value.(string), true, nil
	}
	return "", false, nil
}

func (m *marshaler) MarshalMap(v reflect.Value) (bool, error) {
	m.depth++
	_, err := io.WriteString(m.wr, "{")
	return false, err
}

func (m *marshaler) MarshalMapPost(v reflect.Value) error {
	m.depth--
	if v.Len() > 0 {
		if err := m.writeNewline(); err != nil {
			return err
		}
	}
	_, err := io.WriteString(m.wr, "}")
	return err
}

func (m *marshaler) MarshalMapKey(mv reflect.Value, k string) error {
	if err := m.writeNewline(); err != nil {
		return err
	}
	if _, err := m.writeKey(k); err != nil {
		return err
	}
	_, err := io.WriteString(m.wr, ": ")
	return err
}

func (m *marshaler) MarshalMapValue(mv, v reflect.Value, k string, i int) (bool, error) {
	return false, nil
}

func (m *marshaler) MarshalMapValuePost(mv, v reflect.Value, k string, i int) error {
	if i != mv.Len()-1 || !m.json {
		if _, err := io.WriteString(m.wr, ","); err != nil {
			return err
		}
	}
	return nil
}

func (m *marshaler) ParseStructTag(tag reflect.StructTag) (string, bool) {
	if jsontag, ok := reflectutil.LookupTag(tag, "json", true); ok {
		return jsontag.Value, true
	}
	return "", false
}

func (m *marshaler) MarshalNode(node *syntax.Node) error {
	for _, tok := range node.Tokens {
		if m.json {
			switch tok.Type {
			case syntax.TokenComment, syntax.TokenInlineComment:
				continue
			}
		}
		switch tok.Type {
		case syntax.TokenWhitespace:
			if m.indent != "" {
				continue
			}
			m.newline = false
		case syntax.TokenNewline:
			m.newline = true
			if _, err := io.WriteString(m.wr, m.prefix); err != nil {
				return err
			}
		default:
			if m.newline && m.indent != "" {
				if err := m.writeIndent(m.indent, m.depth); err != nil {
					return err
				}
			}
			m.newline = false
		}
		if _, err := io.WriteString(m.wr, tok.Raw); err != nil {
			return err
		}
	}

	switch node.Type {
	case syntax.NodeMap, syntax.NodeList:
		m.depth++
	}

	return nil
}

func (m *marshaler) MarshalNodePost(node *syntax.Node) error {
	switch node.Type {
	case syntax.NodeMap, syntax.NodeList:
		m.depth--
	}

	var last *syntax.Token
	for i := len(node.Suffix)-1; last == nil && i >= 0; i-- {
		tok := &node.Suffix[i]
		switch tok.Type {
		case syntax.TokenComment, syntax.TokenInlineComment, syntax.TokenNewline, syntax.TokenWhitespace:
			continue
		}
		last = tok
	}

	for _, tok := range node.Suffix {
		if m.json {
			switch tok.Type {
			case syntax.TokenComment, syntax.TokenInlineComment:
				continue
			case TokenComma:
				if last != nil && last.Type != TokenComma {
					continue
				}
			}
		}
		switch tok.Type {
		case syntax.TokenWhitespace:
			if m.indent != "" {
				continue
			}
			m.newline = false
		case syntax.TokenNewline:
			m.newline = true
			if _, err := io.WriteString(m.wr, m.prefix); err != nil {
				return err
			}
		default:
			if m.newline && m.indent != "" {
				if err := m.writeIndent(m.indent, m.depth); err != nil {
					return err
				}
			}
			m.newline = false
		}
		if _, err := io.WriteString(m.wr, tok.Raw); err != nil {
			return err
		}
	}
	return nil
}

var (
	_ reflectutil.Marshaler = (*marshaler)(nil)
	_ reflectutil.PostListMarshaler = (*marshaler)(nil)
	_ reflectutil.PostListElemMarshaler = (*marshaler)(nil)
	_ reflectutil.PostMapMarshaler = (*marshaler)(nil)
	_ reflectutil.PostMapValueMarshaler = (*marshaler)(nil)
	_ reflectutil.Stringifier = (*marshaler)(nil)
	_ reflectutil.StructTagParser = (*marshaler)(nil)
)

func (encoder *Encoder) Encode(v interface{}) error {
	if node, ok := v.(*syntax.Node); ok {
		return node.Marshal(&encoder.marshaler)
	}
	return reflectutil.Marshal(reflect.ValueOf(v), &encoder.marshaler)
}

func MarshalJSON(v interface{}) ([]byte, error) {
	var out bytes.Buffer
	if err := NewEncoder(&out).Option(JSON()).Encode(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func Marshal(v interface{}) ([]byte, error) {
	var out bytes.Buffer
	if err := NewEncoder(&out).Encode(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func MarshalIndent(v interface{}, prefix, indent string) ([]byte, error) {
	var out bytes.Buffer
	if err := NewEncoder(&out).Option(Prefix(prefix), Indent(indent)).Encode(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type EncoderOption func(*Encoder)

func JSON() EncoderOption {
	return func(encoder *Encoder) {
		encoder.marshaler.json = true
	}
}

func Indent(indent string) EncoderOption {
	return func(encoder *Encoder) {
		encoder.marshaler.indent = indent
	}
}

func Prefix(prefix string) EncoderOption {
	return func(encoder *Encoder) {
		encoder.marshaler.prefix = prefix
	}
}

// Save is a convenience function to save the value pointed at by v into a
// JSON5 document at path. It is functionally equivalent to NewEncoder(<file at path>).Encode(v).
func Save(path string, v interface{}) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := NewEncoder(f).Encode(v); err != nil {
		return err
	}
	return f.Close()
}
