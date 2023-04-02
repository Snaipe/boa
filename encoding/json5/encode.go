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

	"snai.pe/boa/encoding"
	"snai.pe/boa/internal/encutil"
	"snai.pe/boa/internal/reflectutil"
	"snai.pe/boa/syntax"
)

type encoder struct {
	marshaler marshaler
}

func NewEncoder(out io.Writer) encoding.Encoder {
	var encoder encoder
	encoder.marshaler.Writer = out
	encoder.marshaler.Self = &encoder.marshaler

	// Defaults
	encoder.marshaler.Indent = "  "
	encoder.marshaler.NamingConvention = encoding.CamelCase
	return &encoder
}

func (encoder *encoder) Encode(v interface{}) error {
	return encoder.marshaler.Encode(v)
}

func (encoder *encoder) Option(opts ...interface{}) encoding.Encoder {
	handle := func(opt interface{}) bool {
		setopt, ok := opt.(EncoderOption)
		if ok {
			setopt(encoder)
		}
		return ok
	}
	if err := encoder.marshaler.Option(handle, opts...); err != nil {
		panic(err)
	}
	return encoder
}

type marshaler struct {
	encutil.MarshalerBase
	structTagParser

	// state
	depth   int
	newline bool

	// options
	json     bool
	prefix   string
	reformat bool
}

// This is mostly like encutil.MarshalerBase.WriteQuote, but with some
// json-specific adjustments
func (m *marshaler) quote(in string, delim rune, json bool) (int, error) {

	written, err := io.WriteString(m.Writer, string(delim))
	if err != nil {
		return written, err
	}

	for _, r := range in {
		switch r {
		case delim, '\n', '\r', '\\', parSep, lineSep:
			n, err := io.WriteString(m.Writer, "\\")
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
			case '\\':
				r = '\\'
			case lineSep:
				n, err := io.WriteString(m.Writer, "u2028")
				if err != nil {
					return written, err
				}
				written += n
				continue
			case parSep:
				n, err := io.WriteString(m.Writer, "u2029")
				if err != nil {
					return written, err
				}
				written += n
				continue
			}
		}
		var buf [utf8.UTFMax]byte
		l := utf8.EncodeRune(buf[:], r)

		n, err := m.Writer.Write(buf[:l])
		written += n
		if err != nil {
			return written, err
		}
	}

	n, err := io.WriteString(m.Writer, string(delim))
	written += n
	if err != nil {
		return written, err
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
		return io.WriteString(m.Writer, s)
	}

	return m.quote(s, '"', false)
}

func (m *marshaler) MarshalValue(v reflect.Value) (bool, error) {
	switch marshaler := v.Interface().(type) {
	case json.Marshaler:
		if v.Kind() == reflect.Ptr && v.IsNil() {
			return true, m.MarshalNil()
		}
		txt, err := marshaler.MarshalJSON()
		if err != nil {
			return false, err
		}

		node, err := newParser(bytes.NewReader(txt)).Parse()
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

func (m *marshaler) MarshalString(s string) error {
	_, err := m.quote(s, '"', true)
	return err
}

func (m *marshaler) MarshalNaN(v float64) error {
	if m.json {
		return fmt.Errorf("NaN is not representable in JSON")
	}
	return m.WriteString("NaN")
}

func (m *marshaler) MarshalInf(v float64) error {
	if m.json {
		return fmt.Errorf("infinity is not representable in JSON")
	}
	var err error
	if v >= 0 {
		_, err = io.WriteString(m.Writer, "+")
	} else {
		_, err = io.WriteString(m.Writer, "-")
	}
	if err == nil {
		_, err = io.WriteString(m.Writer, "Infinity")
	}
	return err
}

func (m *marshaler) MarshalNil() error {
	return m.WriteString("null")
}

func (m *marshaler) MarshalList(v reflect.Value) (bool, error) {
	m.depth++
	if err := m.WriteString("["); err != nil {
		return false, err
	}
	if v.Len() > 0 {
		return false, m.WriteNewline()
	}
	return false, nil
}

func (m *marshaler) MarshalListPost(v reflect.Value) error {
	m.depth--
	if v.Len() > 0 {
		if err := m.WriteIndent(m.depth); err != nil {
			return err
		}
	}
	return m.WriteString("]")
}

func (m *marshaler) MarshalListElem(l, v reflect.Value, i int) (bool, error) {
	return false, m.WriteIndent(m.depth)
}

func (m *marshaler) MarshalListElemPost(l, v reflect.Value, i int) error {
	if i != l.Len()-1 || !m.json {
		if err := m.WriteString(","); err != nil {
			return err
		}
	}
	return m.WriteNewline()
}

func (m *marshaler) Stringify(v reflect.Value) (string, bool, error) {
	switch marshaler := v.Interface().(type) {
	case json.Marshaler:
		txt, err := marshaler.MarshalJSON()
		if err != nil {
			return "", false, err
		}

		node, err := newParser(bytes.NewReader(txt)).Parse()
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

func (m *marshaler) MarshalMap(v reflect.Value, kvs []reflectutil.MapEntry) (bool, error) {
	m.depth++
	if err := m.WriteString("{"); err != nil {
		return false, err
	}
	if reflectutil.Len(v) > 0 {
		return false, m.WriteNewline()
	}
	return false, nil
}

func (m *marshaler) MarshalMapPost(v reflect.Value, kvs []reflectutil.MapEntry) error {
	m.depth--
	if reflectutil.Len(v) > 0 {
		if err := m.WriteIndent(m.depth); err != nil {
			return err
		}
	}
	if err := m.WriteString("}"); err != nil {
		return err
	}
	if m.depth == 0 {
		return m.WriteNewline()
	}
	return nil
}

func (m *marshaler) MarshalMapKey(mv reflect.Value, kv reflectutil.MapEntry, i int) error {
	if err := m.WriteComment("// ", kv.Options.Help, m.depth); err != nil {
		return err
	}
	if err := m.WriteIndent(m.depth); err != nil {
		return err
	}
	if _, err := m.writeKey(kv.Key); err != nil {
		return err
	}
	return m.WriteString(": ")
}

func (m *marshaler) MarshalMapValue(mv reflect.Value, kv reflectutil.MapEntry, i int) (bool, error) {
	return false, nil
}

func (m *marshaler) MarshalMapValuePost(mv reflect.Value, kv reflectutil.MapEntry, i int) error {
	if i != reflectutil.Len(mv)-1 || !m.json {
		m.WriteString(",")
	}
	return m.WriteNewline()
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
			if m.reformat {
				continue
			}
			m.newline = false
		case syntax.TokenNewline:
			m.newline = true
			if err := m.WriteString(m.prefix); err != nil {
				return err
			}
		default:
			if m.newline && m.reformat {
				if err := m.WriteIndent(m.depth); err != nil {
					return err
				}
			}
			m.newline = false
		}
		if err := m.WriteString(tok.Raw); err != nil {
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
	for i := len(node.Suffix) - 1; last == nil && i >= 0; i-- {
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
			if m.reformat {
				continue
			}
			m.newline = false
		case syntax.TokenNewline:
			m.newline = true
			if err := m.WriteString(m.prefix); err != nil {
				return err
			}
		default:
			if m.newline && m.reformat {
				if err := m.WriteIndent(m.depth); err != nil {
					return err
				}
			}
			m.newline = false
		}
		if err := m.WriteString(tok.Raw); err != nil {
			return err
		}
	}
	return nil
}

type structTagParser struct{}

func (structTagParser) ParseStructTag(tag reflect.StructTag) (reflectutil.FieldOpts, bool) {
	var opts reflectutil.FieldOpts
	if jsontag, ok := reflectutil.LookupTag(tag, "json", true); ok {
		if opts.Name == "-" {
			opts.Ignore = true
		} else {
			opts.Name = jsontag.Value
		}
		return opts, true
	}
	return opts, false
}

var (
	_ reflectutil.Marshaler             = (*marshaler)(nil)
	_ reflectutil.PostListMarshaler     = (*marshaler)(nil)
	_ reflectutil.PostListElemMarshaler = (*marshaler)(nil)
	_ reflectutil.PostMapMarshaler      = (*marshaler)(nil)
	_ reflectutil.PostMapValueMarshaler = (*marshaler)(nil)
	_ reflectutil.Stringifier           = (*marshaler)(nil)
	_ reflectutil.StructTagParser       = (*marshaler)(nil)
	_ reflectutil.NaNMarshaler          = (*marshaler)(nil)
	_ reflectutil.InfMarshaler          = (*marshaler)(nil)
	_ reflectutil.NilMarshaler          = (*marshaler)(nil)
)

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
	enc := NewEncoder(&out).(*encoder)
	enc.marshaler.prefix = prefix
	enc.marshaler.Indent = indent
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type EncoderOption func(*encoder)

func JSON() EncoderOption {
	return func(encoder *encoder) {
		encoder.marshaler.json = true
	}
}

func Prefix(prefix string) EncoderOption {
	return func(encoder *encoder) {
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
