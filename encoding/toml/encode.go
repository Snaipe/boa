// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package toml

import (
	"bytes"
	"fmt"
	"go/constant"
	"io"
	"math/big"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
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
			wr:       wr,
			first:    true,
			curdepth: -1,
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
		default:
			panic(fmt.Sprintf("%T is not a common option, nor an encoder option.", opt))
		}
	}
	return encoder
}

type marshaler struct {
	structTagParser

	// state
	wr        io.Writer
	first     bool
	curdepth  int
	listdepth int
	valdepth  int
	inline    bool
	inlinerun bool
	newline   bool
	path      []string
	listofmap []bool

	// options
	encoding.CommonOptions
	encoding.EncoderOptions
}

func (m *marshaler) depth(offset int) int {
	// Because of the weird indentation rules, the top-level table
	// has the same indentation level as its subtables. Therefore,
	// depth -1 must be represented as depth 0 for values.
	if m.curdepth + offset < 0 {
		return 0
	}
	return m.curdepth + offset
}

func (m *marshaler) quote(in string, delim rune) (int, error) {
	written := 0

	n, err := io.WriteString(m.wr, string(delim))
	written += n
	if err != nil {
		return written, err
	}

	for _, r := range in {
		switch r {
		case delim, '\n', '\r', '\b', '\t', '\f', '\\':
			n, err := io.WriteString(m.wr, "\\")
			written += n
			if err != nil {
				return written, err
			}
			switch r {
			case '\n':
				r = 'n'
			case '\r':
				r = 'r'
			case '\b':
				r = 'b'
			case '\t':
				r = 't'
			case '\f':
				r = 'f'
			}
		}
		var n int
		if unicode.IsPrint(r) {
			var buf [utf8.UTFMax]byte
			l := utf8.EncodeRune(buf[:], r)

			n, err = m.wr.Write(buf[:l])
		} else if r > 0xffff {
			n, err = fmt.Fprintf(m.wr, "\\U%08x", r)
		} else {
			n, err = fmt.Fprintf(m.wr, "\\u%04x", r)
		}
		written += n
		if err != nil {
			return written, err
		}
	}

	n, err = io.WriteString(m.wr, string(delim))
	written += n
	if err != nil {
		return written, err
	}

	return written, nil
}

func (m *marshaler) writeKey(s string) (int, error) {
	isNotIdChar := func(r rune) bool {
		return !isIdentifierChar(r)
	}

	if strings.IndexFunc(s, isNotIdChar) == -1 && len(s) > 0 {
		// The key contains legal identifier characters -- we can emit it
		// as-is without quoting.
		return io.WriteString(m.wr, s)
	}

	return m.quote(s, '"')
}

func (m *marshaler) writeKeyPath(s []string) error {
	for i, e := range s {
		if _, err := m.writeKey(e); err != nil {
			return err
		}
		if i != len(s)-1 {
			if _, err := io.WriteString(m.wr, "."); err != nil {
				return err
			}
		}
	}
	return nil
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

func isValueType(v reflect.Value) bool {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if !v.IsValid() {
		// nil interfaces are values, though not marshalable.
		return true
	}
	t := v.Type()
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if reflectutil.IsValueType(t) {
		return true
	}
	if reflectutil.IsList(t) {
		if reflectutil.IsMap(t.Elem()) && !isValueType(reflect.Zero(t.Elem())) {
			return false
		}
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			for elem.Kind() == reflect.Interface || elem.Kind() == reflect.Ptr {
				elem = elem.Elem()
			}
			ok := isValueType(elem)
			if ok || reflectutil.IsList(elem.Type()) {
				return true
			}
		}
		return v.Len() == 0
	}
	switch t.Kind() {
	case reflect.Map:
		return false
	case reflect.Struct:
		switch t {
		case reflect.TypeOf(time.Time{}),
			reflect.TypeOf(LocalDateTime{}),
			reflect.TypeOf(LocalDate{}),
			reflect.TypeOf(LocalTime{}):
			return true
		}
		return false
	}
	return true
}

func (m *marshaler) MarshalValue(v reflect.Value) (bool, error) {
	if m.valdepth == 0 && isValueType(v) {
		return false, fmt.Errorf("cannot marshal single value %v: document must contain tables, but %T is not a map or struct.", v.Interface(), v.Interface())
	}
	switch val := v.Interface().(type) {
	case *time.Time, time.Time:
		type Formatter interface {
			Format(layout string) string
		}
		_, err := io.WriteString(m.wr, val.(Formatter).Format(time.RFC3339Nano))
		return true, err
	case LocalDateTime, LocalDate, LocalTime:
		_, err := io.WriteString(m.wr, val.(fmt.Stringer).String())
		return true, err
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
	_, err := m.quote(s, '"')
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
	_, err := io.WriteString(m.wr, "nan")
	return err
}

func (m *marshaler) MarshalInf(v float64) error {
	var err error
	if v >= 0 {
		_, err = io.WriteString(m.wr, "+")
	} else {
		_, err = io.WriteString(m.wr, "-")
	}
	if err == nil {
		_, err = io.WriteString(m.wr, "inf")
	}
	return err
}

func (m *marshaler) MarshalList(v reflect.Value) (bool, error) {
	ok := m.valdepth > 0 || isValueType(v)
	m.listofmap = append(m.listofmap, !ok)

	if ok {
		m.listdepth++
		if _, err := io.WriteString(m.wr, "["); err != nil {
			return false, err
		}
		if v.Len() > 0 {
			if m.valdepth == 1 {
				if _, err := io.WriteString(m.wr, "\n"); err != nil {
					return false, err
				}
			} else {
				if _, err := io.WriteString(m.wr, " "); err != nil {
					return false, err
				}
			}
		}
		return false, nil
	}
	return false, nil
}

func (m *marshaler) MarshalListPost(v reflect.Value) error {
	listofmap := m.listofmap[len(m.listofmap)-1]
	m.listofmap = m.listofmap[:len(m.listofmap)-1]
	if !listofmap {
		m.listdepth--
		if m.valdepth == 1 {
			// Only indent elements if we're not nested in a submap
			if err := m.writeIndent(m.Indent, m.depth(0)+m.listdepth); err != nil {
				return err
			}
		}
		_, err := io.WriteString(m.wr, "]")
		return err
	}
	return nil
}

func (m *marshaler) MarshalListElem(l, v reflect.Value, i int) (bool, error) {
	listofmap := m.listofmap[len(m.listofmap)-1]
	if !listofmap {
		if m.valdepth == 1 {
			// Only indent elements if we're not nested in a submap
			if err := m.writeIndent(m.Indent, m.depth(0)+m.listdepth); err != nil {
				return false, err
			}
		}
	} else {
		if !m.first {
			if _, err := io.WriteString(m.wr, "\n"); err != nil {
				return false, err
			}
			m.first = false
		}
		if err := m.writeIndent(m.Indent, m.depth(1)); err != nil {
			return false, err
		}
		if _, err := io.WriteString(m.wr, "[["); err != nil {
			return false, err
		}
		if err := m.writeKeyPath(m.path); err != nil {
			return false, err
		}
		_, err := io.WriteString(m.wr, "]]\n")
		m.curdepth++
		return false, err
	}
	return false, nil
}

func (m *marshaler) MarshalListElemPost(l, v reflect.Value, i int) error {
	listofmap := m.listofmap[len(m.listofmap)-1]
	if !listofmap {
		// Always emit a trailing comma when listing one element per line,
		// otherwise omit when it's the last element of the list.
		if m.valdepth == 1 || i != l.Len() - 1 {
			if _, err := io.WriteString(m.wr, ","); err != nil {
				return err
			}
		}
		// Only emit a newline if we're not nested in a submap; otherwise,
		// keep everything on the same line, separated by spaces.
		if m.valdepth == 1 {
			if _, err := io.WriteString(m.wr, "\n"); err != nil {
				return err
			}
		} else {
			if _, err := io.WriteString(m.wr, " "); err != nil {
				return err
			}
		}
	} else {
		m.curdepth--
	}
	return nil
}

func (m *marshaler) Stringify(v reflect.Value) (string, bool, error) {
	return "", false, nil
}

func (m *marshaler) MarshalMap(mv reflect.Value, kvs []reflectutil.MapEntry) (bool, error) {
	if m.inline {
		if _, err := io.WriteString(m.wr, "{"); err != nil {
			return false, err
		}
		if _, err := io.WriteString(m.wr, " "); err != nil {
			return false, err
		}
	} else {
		// The TOML marshalling logic for maps is a bit special; we need to sort
		// keys by lexical order, but also arrange keys so that subtable definitions
		// appear after all of the other keys.

		sort.SliceStable(kvs, func(i, j int) bool {
			t1 := !isValueType(kvs[i].Value)
			t2 := !isValueType(kvs[j].Value)
			if t1 != t2 {
				return t2
			}
			return false
		})
	}
	return false, nil
}

func (m *marshaler) MarshalMapPost(v reflect.Value, kvs []reflectutil.MapEntry) error {
	if m.inline {
		if _, err := io.WriteString(m.wr, "}"); err != nil {
			return err
		}
	}
	return nil
}

func (m *marshaler) writeComment(comments []string, depth int) error {
	for _, comment := range comments {
		if err := m.writeIndent(m.Indent, depth); err != nil {
			return err
		}
		if _, err := io.WriteString(m.wr, "# "); err != nil {
			return err
		}
		if _, err := io.WriteString(m.wr, comment); err != nil {
			return err
		}
		if _, err := io.WriteString(m.wr, "\n"); err != nil {
			return err
		}
	}
	return nil
}

func (m *marshaler) MarshalMapKey(mv reflect.Value, kv reflectutil.MapEntry, i int) error {
	m.path = append(m.path, kv.Key)
	v := kv.Value

	if m.valdepth > 0 || isValueType(v) {
		if m.valdepth == 0 {
			if err := m.writeComment(kv.Options.Help, m.depth(0)); err != nil {
				return err
			}
			if err := m.writeIndent(m.Indent, m.depth(0)); err != nil {
				return err
			}
		}
		if _, err := m.writeKey(kv.Key); err != nil {
			return err
		}
		if _, err := io.WriteString(m.wr, " = "); err != nil {
			return err
		}
		m.first = false
	} else {
		for v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
			v = v.Elem()
		}
		if !reflectutil.IsList(v.Type()) {
			if !m.first {
				if _, err := io.WriteString(m.wr, "\n"); err != nil {
					return err
				}
				m.first = false
			}
			if err := m.writeComment(kv.Options.Help, m.depth(1)); err != nil {
				return err
			}
			if err := m.writeIndent(m.Indent, m.depth(1)); err != nil {
				return err
			}
			if _, err := io.WriteString(m.wr, "["); err != nil {
				return err
			}
			if err := m.writeKeyPath(m.path); err != nil {
				return err
			}
			if _, err := io.WriteString(m.wr, "]\n"); err != nil {
				return err
			}
			m.curdepth++
		} else {
			if err := m.writeComment(kv.Options.Help, m.depth(1)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *marshaler) MarshalMapValue(mv reflect.Value, kv reflectutil.MapEntry, i int) (bool, error) {
	v := kv.Value
	if m.valdepth > 0 || isValueType(v) {
		m.valdepth++
		m.inline = true
	}
	return false, nil
}

func (m *marshaler) MarshalMapValuePost(mv reflect.Value, kv reflectutil.MapEntry, i int) error {
	m.path = m.path[:len(m.path)-1]
	v := kv.Value

	if m.valdepth > 0 || isValueType(v) {
		m.valdepth--
		if m.valdepth == 0 {
			m.inline = false
			if _, err := io.WriteString(m.wr, "\n"); err != nil {
				return err
			}
		} else if i != mv.Len()-1 {
			if _, err := io.WriteString(m.wr, ", "); err != nil {
				return err
			}
		} else {
			if _, err := io.WriteString(m.wr, " "); err != nil {
				return err
			}
		}
	} else {
		for v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
			v = v.Elem()
		}
		if !reflectutil.IsList(v.Type()) {
			m.curdepth--
		}
	}
	return nil
}

func (m *marshaler) MarshalNode(node *syntax.Node) error {
	for _, tok := range node.Tokens {
		if _, err := io.WriteString(m.wr, tok.Raw); err != nil {
			return err
		}
	}
	return nil
}

func (m *marshaler) MarshalNodePost(node *syntax.Node) error {
	for _, tok := range node.Suffix {
		if _, err := io.WriteString(m.wr, tok.Raw); err != nil {
			return err
		}
	}
	return nil
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
)

type structTagParser struct{}

func (structTagParser) ParseStructTag(tag reflect.StructTag) (reflectutil.FieldOpts, bool) {
	var opts reflectutil.FieldOpts
	if tomltag, ok := reflectutil.LookupTag(tag, "toml", true); ok {
		if tomltag.Value == "-" {
			opts.Ignore = true
		} else {
			opts.Name = tomltag.Value
		}
		return opts, true
	}
	return opts, false
}

func (encoder *Encoder) Encode(v interface{}) error {
	if node, ok := v.(*syntax.Node); ok {
		return node.Marshal(&encoder.marshaler)
	}

	convention := encoder.marshaler.NamingConvention
	if convention == nil {
		convention = encoding.SnakeCase
	}

	return reflectutil.Marshal(reflect.ValueOf(v), &encoder.marshaler, convention)
}

func Marshal(v interface{}) ([]byte, error) {
	var out bytes.Buffer
	if err := NewEncoder(&out).Encode(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type EncoderOption func(*Encoder)

// Save is a convenience function to save the value pointed at by v into a
// TOML document at path. It is functionally equivalent to NewEncoder(<file at path>).Encode(v).
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
