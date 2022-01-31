// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package json5

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/constant"
	"io"
	"math/big"
	"os"
	"reflect"
	"strings"
	"unicode/utf8"

	"snai.pe/boa/encoding"
	"snai.pe/boa/internal/reflectutil"
	"snai.pe/boa/syntax"
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

func (encoder *Encoder) Option(opts ...interface{}) encoding.Encoder {
	for _, opt := range opts {
		switch setopt := opt.(type) {
		case encoding.CommonOption:
			setopt(&encoder.marshaler.CommonOptions)
		case encoding.EncoderOption:
			setopt(&encoder.marshaler.EncoderOptions)
		case EncoderOption:
			setopt(encoder)
		default:
			panic(fmt.Sprintf("%T is not a common option, nor an encoder option.", opt))
		}
	}
	return encoder
}

type runeWriter interface {
	Write([]byte) (int, error)
	WriteRune(r rune) (int, error)
}

type marshaler struct {
	structTagParser

	// state
	wr      io.Writer
	depth   int
	newline bool

	// options
	encoding.CommonOptions
	encoding.EncoderOptions
	json   bool
	prefix string
}

func (m *marshaler) quote(in string, delim rune, json bool) (int, error) {

	written, err := io.WriteString(m.wr, string(delim))
	if err != nil {
		return written, err
	}

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

	n, err := io.WriteString(m.wr, string(delim))
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
		return io.WriteString(m.wr, s)
	}

	return m.quote(s, '"', false)
}

func (m *marshaler) writeNewline() error {
	if _, err := io.WriteString(m.wr, "\n"); err != nil {
		return err
	}
	return m.writeIndent(m.Indent, m.depth)
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

func (m *marshaler) MarshalBool(b bool) (err error) {
	if b {
		_, err = io.WriteString(m.wr, "true")
	} else {
		_, err = io.WriteString(m.wr, "false")
	}
	return err
}

func (m *marshaler) MarshalString(s string) error {
	_, err := m.quote(s, '"', true)
	return err
}

func (m *marshaler) MarshalNumber(v constant.Value) (err error) {
	switch v.Kind() {
	case constant.Int:
		_, err = fmt.Fprintf(m.wr, "%d", constant.Val(v))
	case constant.Float:
		val := constant.Val(v)

		var out strings.Builder
		if rat, ok := val.(*big.Rat); ok {
			val, _ = rat.Float64()
		}
		_, err = fmt.Fprintf(&out, "%g", val)

		// Preserve at least a trailing .0 to keep floats as floats.
		if !strings.ContainsAny(out.String(), ".eE") {
			out.WriteString(".0")
		}
		io.WriteString(m.wr, out.String())
	default:
		err = fmt.Errorf("unsupported constant %v", v)
	}
	return err
}

func (m *marshaler) MarshalNaN(v float64) error {
	if m.json {
		return fmt.Errorf("NaN is not representable in JSON")
	}
	_, err := io.WriteString(m.wr, "NaN")
	return err
}

func (m *marshaler) MarshalInf(v float64) error {
	if m.json {
		return fmt.Errorf("infinity is not representable in JSON")
	}
	var err error
	if v >= 0 {
		_, err = io.WriteString(m.wr, "+")
	} else {
		_, err = io.WriteString(m.wr, "-")
	}
	if err == nil {
		_, err = io.WriteString(m.wr, "Infinity")
	}
	return err
}

func (m *marshaler) MarshalNil() error {
	_, err := io.WriteString(m.wr, "null")
	return err
}

func (m *marshaler) MarshalList(v reflect.Value) (bool, error) {
	m.depth++
	if _, err := io.WriteString(m.wr, "["); err != nil {
		return false, err
	}
	if v.Len() > 0 {
		if _, err := io.WriteString(m.wr, "\n"); err != nil {
			return false, err
		}
	}
	return false, nil
}

func (m *marshaler) MarshalListPost(v reflect.Value) error {
	m.depth--
	if v.Len() > 0 {
		if err := m.writeIndent(m.Indent, m.depth); err != nil {
			return err
		}
	}
	_, err := io.WriteString(m.wr, "]")
	return err
}

func (m *marshaler) MarshalListElem(l, v reflect.Value, i int) (bool, error) {
	return false, m.writeIndent(m.Indent, m.depth)
}

func (m *marshaler) MarshalListElemPost(l, v reflect.Value, i int) error {
	if i != l.Len()-1 || !m.json {
		if _, err := io.WriteString(m.wr, ","); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(m.wr, "\n"); err != nil {
		return err
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

func (m *marshaler) MarshalMap(v reflect.Value, kvs []reflectutil.MapEntry) (bool, error) {
	m.depth++
	if _, err := io.WriteString(m.wr, "{"); err != nil {
		return false, err
	}
	if reflectutil.Len(v) > 0 {
		if _, err := io.WriteString(m.wr, "\n"); err != nil {
			return false, err
		}
	}
	return false, nil
}

func (m *marshaler) MarshalMapPost(v reflect.Value, kvs []reflectutil.MapEntry) error {
	m.depth--
	if reflectutil.Len(v) > 0 {
		if err := m.writeIndent(m.Indent, m.depth); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(m.wr, "}"); err != nil {
		return err
	}
	if m.depth == 0 {
		if _, err := io.WriteString(m.wr, "\n"); err != nil {
			return err
		}
	}
	return nil
}

func (m *marshaler) MarshalMapKey(mv reflect.Value, kv reflectutil.MapEntry, i int) error {
	if err := m.writeIndent(m.Indent, m.depth); err != nil {
		return err
	}
	for _, comment := range kv.Options.Help {
		if _, err := io.WriteString(m.wr, "// "); err != nil {
			return err
		}
		if _, err := io.WriteString(m.wr, comment); err != nil {
			return err
		}
		if err := m.writeNewline(); err != nil {
			return err
		}
	}
	if _, err := m.writeKey(kv.Key); err != nil {
		return err
	}
	_, err := io.WriteString(m.wr, ": ")
	return err
}

func (m *marshaler) MarshalMapValue(mv reflect.Value, kv reflectutil.MapEntry, i int) (bool, error) {
	return false, nil
}

func (m *marshaler) MarshalMapValuePost(mv reflect.Value, kv reflectutil.MapEntry, i int) error {
	if i != reflectutil.Len(mv)-1 || !m.json {
		if _, err := io.WriteString(m.wr, ",\n"); err != nil {
			return err
		}
	}
	return nil
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
			if m.Indent != "" {
				continue
			}
			m.newline = false
		case syntax.TokenNewline:
			m.newline = true
			if _, err := io.WriteString(m.wr, m.prefix); err != nil {
				return err
			}
		default:
			if m.newline && m.Indent != "" {
				if err := m.writeIndent(m.Indent, m.depth); err != nil {
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
			if m.Indent != "" {
				continue
			}
			m.newline = false
		case syntax.TokenNewline:
			m.newline = true
			if _, err := io.WriteString(m.wr, m.prefix); err != nil {
				return err
			}
		default:
			if m.newline && m.Indent != "" {
				if err := m.writeIndent(m.Indent, m.depth); err != nil {
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

func (encoder *Encoder) Encode(v interface{}) error {
	if node, ok := v.(*syntax.Node); ok {
		return node.Marshal(&encoder.marshaler)
	}

	convention := encoder.marshaler.NamingConvention
	if convention == nil {
		convention = encoding.CamelCase
	}

	return reflectutil.Marshal(reflect.ValueOf(v), &encoder.marshaler, convention)
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
	enc := NewEncoder(&out)
	enc.marshaler.prefix = prefix
	enc.marshaler.Indent = indent
	if err := enc.Encode(v); err != nil {
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
