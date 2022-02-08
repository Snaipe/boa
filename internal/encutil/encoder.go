// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package encutil

import (
	"fmt"
	"go/constant"
	"io"
	"math/big"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"snai.pe/boa/encoding"
	"snai.pe/boa/internal/reflectutil"
	"snai.pe/boa/syntax"
)

type Marshaler interface {
	reflectutil.Marshaler
	syntax.Marshaler
}

type MarshalerBase struct {
	Writer io.Writer
	Self   Marshaler

	encoding.CommonOptions
	encoding.EncoderOptions
}

func (m *MarshalerBase) Encode(v interface{}) error {
	if node, ok := v.(*syntax.Node); ok {
		return node.Marshal(m.Self)
	}
	return reflectutil.Marshal(reflect.ValueOf(v), m.Self, m.NamingConvention)
}

func (m *MarshalerBase) Option(handle func(interface{}) bool, opts ...interface{}) error {
	for _, opt := range opts {
		switch setopt := opt.(type) {
		case encoding.CommonOption:
			setopt(&m.CommonOptions)
		case encoding.EncoderOption:
			setopt(&m.EncoderOptions)
		default:
			if handle != nil {
				if ok := handle(opt); ok {
					continue
				}
			}
			return fmt.Errorf("%T is not a common option, nor an encoder option.", opt)
		}
	}
	return nil
}

func (m *MarshalerBase) WriteString(s string) error {
	_, err := io.WriteString(m.Writer, s)
	return err
}

func (m *MarshalerBase) WriteNewline() error {
	nl := m.LineBreak
	if nl == "" {
		nl = "\n"
	}
	_, err := io.WriteString(m.Writer, nl)
	return err
}

func (m *MarshalerBase) WriteIndent(level int) error {
	for i := 0; i < level; i++ {
		if _, err := io.WriteString(m.Writer, m.Indent); err != nil {
			return err
		}
	}
	return nil
}

func (m *MarshalerBase) WriteComment(prefix string, comments []string, depth int) error {
	for _, comment := range comments {
		if err := m.WriteIndent(depth); err != nil {
			return err
		}
		if err := m.WriteString(prefix); err != nil {
			return err
		}
		if err := m.WriteString(comment); err != nil {
			return err
		}
		if err := m.WriteString("\n"); err != nil {
			return err
		}
	}
	return nil
}

func (m *MarshalerBase) WriteQuoted(in string, delim rune) error {
	if err := m.WriteString(string(delim)); err != nil {
		return err
	}

	for _, r := range in {
		switch r {
		case delim, '\n', '\r', '\b', '\t', '\f', '\\':
			if err := m.WriteString("\\"); err != nil {
				return err
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
		var err error
		if unicode.IsPrint(r) {
			var buf [utf8.UTFMax]byte
			l := utf8.EncodeRune(buf[:], r)

			_, err = m.Writer.Write(buf[:l])
		} else if r > 0xffff {
			_, err = fmt.Fprintf(m.Writer, "\\U%08x", r)
		} else {
			_, err = fmt.Fprintf(m.Writer, "\\u%04x", r)
		}
		if err != nil {
			return err
		}
	}

	return m.WriteString(string(delim))
}

func (m *MarshalerBase) MarshalBool(b bool) error {
	if b {
		return m.WriteString("true")
	} else {
		return m.WriteString("false")
	}
}

func (m *MarshalerBase) MarshalString(s string) error {
	return m.WriteQuoted(s, '"')
}

func (m *MarshalerBase) MarshalNumber(v constant.Value) (err error) {
	switch v.Kind() {
	case constant.Int:
		_, err = fmt.Fprintf(m.Writer, "%d", constant.Val(v))
	case constant.Float:
		val := constant.Val(v)

		var out strings.Builder
		if rat, ok := val.(*big.Rat); ok {
			val, _ = rat.Float64()
		}
		if _, err := fmt.Fprintf(&out, "%g", val); err != nil {
			return err
		}

		// Preserve at least a trailing .0 to keep floats as floats.
		if !strings.ContainsAny(out.String(), ".eE") {
			out.WriteString(".0")
		}
		err = m.WriteString(out.String())
	default:
		err = fmt.Errorf("unsupported constant %v", v)
	}
	return err
}
