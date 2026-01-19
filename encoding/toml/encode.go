// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package toml

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"snai.pe/boa/encoding"
	"snai.pe/boa/internal/encutil"
	"snai.pe/boa/internal/reflectutil"
	"snai.pe/boa/syntax"
)

type encoder struct {
	marshaler marshaler
}

func NewEncoder(out io.Writer) encoding.Encoder {
	encoder := encoder{
		marshaler: marshaler{
			first:    true,
			curdepth: -1,
		},
	}
	encoder.marshaler.Writer = out
	encoder.marshaler.Self = &encoder.marshaler

	// Defaults
	encoder.marshaler.Indent = "  "
	encoder.marshaler.NamingConvention = encoding.SnakeCase
	return &encoder
}

func (encoder *encoder) Encode(v interface{}) error {
	return encoder.marshaler.Encode(v)
}

func (encoder *encoder) Option(opts ...interface{}) encoding.Encoder {
	if err := encoder.marshaler.Option(nil, opts...); err != nil {
		panic(err)
	}
	return encoder
}

type marshaler struct {
	encutil.MarshalerBase
	structTagParser

	// state
	first     bool
	curdepth  int
	listdepth int
	valdepth  int
	inline    bool
	inlinerun bool
	newline   bool
	path      []string
	listofmap []bool
}

func (m *marshaler) depth(offset int) int {
	// Because of the weird indentation rules, the top-level table
	// has the same indentation level as its subtables. Therefore,
	// depth -1 must be represented as depth 0 for values.
	if m.curdepth+offset < 0 {
		return 0
	}
	return m.curdepth + offset
}

func (m *marshaler) writeKey(s string) error {
	isNotIdChar := func(r rune) bool {
		return !isIdentifierChar(r)
	}

	if strings.IndexFunc(s, isNotIdChar) == -1 && len(s) > 0 {
		// The key contains legal identifier characters -- we can emit it
		// as-is without quoting.
		return m.WriteString(s)
	}
	return m.WriteQuoted(s, '"')
}

func (m *marshaler) writeKeyPath(s []string) error {
	for i, e := range s {
		if err := m.writeKey(e); err != nil {
			return err
		}
		if i != len(s)-1 {
			if err := m.WriteString("."); err != nil {
				return err
			}
		}
	}
	return nil
}

func isValueType(v reflect.Value) bool {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if !v.IsValid() {
		// nil interfaces are values, though not marshalable.
		return true
	}
	t := v.Type()
	for t.Kind() == reflect.Pointer {
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
			for elem.Kind() == reflect.Interface || elem.Kind() == reflect.Pointer {
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
		return true, m.WriteString(val.(Formatter).Format(time.RFC3339Nano))
	case LocalDateTime, LocalDate, LocalTime:
		return true, m.WriteString(val.(fmt.Stringer).String())
	}
	return false, nil
}

func (m *marshaler) MarshalNaN(v float64) error {
	return m.WriteString("nan")
}

func (m *marshaler) MarshalInf(v float64) error {
	var err error
	if v >= 0 {
		err = m.WriteString("+")
	} else {
		err = m.WriteString("-")
	}
	if err == nil {
		err = m.WriteString("inf")
	}
	return err
}

func (m *marshaler) MarshalList(v reflect.Value) (bool, error) {
	ok := m.valdepth > 0 || isValueType(v)
	m.listofmap = append(m.listofmap, !ok)

	if ok {
		m.listdepth++
		if err := m.WriteString("["); err != nil {
			return false, err
		}
		if v.Len() > 0 {
			if m.valdepth == 1 {
				if err := m.WriteNewline(); err != nil {
					return false, err
				}
			} else {
				if err := m.WriteString(" "); err != nil {
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
			if err := m.WriteIndent(m.depth(0) + m.listdepth); err != nil {
				return err
			}
		}
		err := m.WriteString("]")
		return err
	}
	return nil
}

func (m *marshaler) MarshalListElem(l, v reflect.Value, i int) (bool, error) {
	listofmap := m.listofmap[len(m.listofmap)-1]
	if !listofmap {
		if m.valdepth == 1 {
			// Only indent elements if we're not nested in a submap
			if err := m.WriteIndent(m.depth(0) + m.listdepth); err != nil {
				return false, err
			}
		}
	} else {
		if !m.first {
			if err := m.WriteNewline(); err != nil {
				return false, err
			}
			m.first = false
		}
		if err := m.WriteIndent(m.depth(1)); err != nil {
			return false, err
		}
		if err := m.WriteString("[["); err != nil {
			return false, err
		}
		if err := m.writeKeyPath(m.path); err != nil {
			return false, err
		}
		err := m.WriteString("]]\n")
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
		if m.valdepth == 1 || i != l.Len()-1 {
			if err := m.WriteString(","); err != nil {
				return err
			}
		}
		// Only emit a newline if we're not nested in a submap; otherwise,
		// keep everything on the same line, separated by spaces.
		if m.valdepth == 1 {
			if err := m.WriteNewline(); err != nil {
				return err
			}
		} else {
			if err := m.WriteString(" "); err != nil {
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
		if err := m.WriteString("{"); err != nil {
			return false, err
		}
		if err := m.WriteString(" "); err != nil {
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
		if err := m.WriteString("}"); err != nil {
			return err
		}
	}
	return nil
}

func (m *marshaler) writeComment(comments []string, depth int) error {
	return m.WriteComment("# ", comments, depth)
}

func (m *marshaler) MarshalMapKey(mv reflect.Value, kv reflectutil.MapEntry, i int) error {
	m.path = append(m.path, kv.Key)
	v := kv.Value

	if m.valdepth > 0 || isValueType(v) {
		if m.valdepth == 0 {
			if err := m.writeComment(kv.Options.Help, m.depth(0)); err != nil {
				return err
			}
			if err := m.WriteIndent(m.depth(0)); err != nil {
				return err
			}
		}
		if err := m.writeKey(kv.Key); err != nil {
			return err
		}
		if err := m.WriteString(" = "); err != nil {
			return err
		}
		m.first = false
	} else {
		for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if !reflectutil.IsList(v.Type()) {
			if !m.first {
				if err := m.WriteNewline(); err != nil {
					return err
				}
				m.first = false
			}
			if err := m.writeComment(kv.Options.Help, m.depth(1)); err != nil {
				return err
			}
			if err := m.WriteIndent(m.depth(1)); err != nil {
				return err
			}
			if err := m.WriteString("["); err != nil {
				return err
			}
			if err := m.writeKeyPath(m.path); err != nil {
				return err
			}
			if err := m.WriteString("]\n"); err != nil {
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
			if err := m.WriteNewline(); err != nil {
				return err
			}
		} else if i != mv.Len()-1 {
			if err := m.WriteString(", "); err != nil {
				return err
			}
		} else {
			if err := m.WriteString(" "); err != nil {
				return err
			}
		}
	} else {
		for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if !reflectutil.IsList(v.Type()) {
			m.curdepth--
		}
	}
	return nil
}

func (m *marshaler) MarshalStructValuePost(mv reflect.Value, kv reflectutil.MapEntry, i int) error {
	return m.MarshalMapValuePost(mv, kv, i)
}

func (m *marshaler) MarshalNode(node *syntax.Node) error {
	for _, tok := range node.Tokens {
		if err := m.WriteString(tok.Raw); err != nil {
			return err
		}
	}
	return nil
}

func (m *marshaler) MarshalNodePost(node *syntax.Node) error {
	for _, tok := range node.Suffix {
		if err := m.WriteString(tok.Raw); err != nil {
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

func Marshal(v interface{}) ([]byte, error) {
	var out bytes.Buffer
	if err := NewEncoder(&out).Encode(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

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
