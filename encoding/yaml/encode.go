// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package yaml

import (
	"bytes"
	"io"
	"os"
	"reflect"
	"strings"

	"snai.pe/boa/encoding"
	"snai.pe/boa/internal/encutil"
	"snai.pe/boa/internal/reflectutil"
	. "snai.pe/boa/syntax"
)

// newlineTracker wraps an io.Writer and tracks whether the last byte written
// was a newline, so the encoder can avoid emitting duplicate blank lines.
type newlineTracker struct {
	io.Writer
	lastWasNewline bool
}

func (w *newlineTracker) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if n > 0 {
		w.lastWasNewline = p[n-1] == '\n'
	}
	return n, err
}

type encoder struct {
	marshaler marshaler
}

func NewEncoder(out io.Writer) encoding.Encoder {
	var enc encoder
	enc.marshaler.tracker = &newlineTracker{Writer: out}
	enc.marshaler.Writer = enc.marshaler.tracker
	enc.marshaler.Self = &enc.marshaler
	enc.marshaler.StructTagParser = encutil.StructTagParser{Tag: "yaml"}
	enc.marshaler.Indent = "  "
	enc.marshaler.NamingConvention = encoding.KebabCase
	return &enc
}

func (enc *encoder) Encode(v interface{}) error {
	return enc.marshaler.Encode(v)
}

func (enc *encoder) Option(opts ...interface{}) encoding.Encoder {
	handle := func(opt interface{}) bool {
		setopt, ok := opt.(EncoderOption)
		if ok {
			setopt(enc)
		}
		return ok
	}
	if err := enc.marshaler.Option(handle, opts...); err != nil {
		panic(err)
	}
	return enc
}

type marshaler struct {
	encutil.MarshalerBase
	encutil.StructTagParser

	tracker   *newlineTracker
	depth     int
	inlineVal bool // true when the current map value is a scalar (inline after ": ")
}

// isCompound reports whether v is a non-empty collection that should be
// rendered in YAML block style on a new indented line.
func isCompound(v reflect.Value) bool {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		return v.Len() > 0
	case reflect.Map:
		return v.Len() > 0
	case reflect.Struct:
		return v.NumField() > 0
	}
	return false
}

// needsQuoting reports whether a string scalar must be quoted in YAML.
func needsQuoting(s string) bool {
	if len(s) == 0 {
		return true
	}
	// YAML core-schema keywords that would be misinterpreted as non-strings.
	switch strings.ToLower(s) {
	case "true", "false", "yes", "no", "on", "off", "null", "~",
		".inf", "-.inf", "+.inf", ".nan":
		return true
	}
	// Characters that start YAML special constructs.
	switch s[0] {
	case '-', '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!',
		'|', '>', '\'', '"', '%', '@', '`', ' ', '\t':
		return true
	}
	for _, r := range s {
		switch r {
		case ':', '#', '[', ']', '{', '}', ',', '\n', '\r', '\t':
			return true
		}
	}
	return false
}

func (m *marshaler) writeKey(s string) error {
	if needsQuoting(s) {
		return m.WriteQuoted(s, '"')
	}
	return m.WriteString(s)
}

func (m *marshaler) MarshalValue(v reflect.Value) (bool, error) {
	return false, nil
}

func (m *marshaler) MarshalString(s string) error {
	if needsQuoting(s) {
		return m.WriteQuoted(s, '"')
	}
	return m.WriteString(s)
}

func (m *marshaler) MarshalNil() error {
	return m.WriteString("null")
}

func (m *marshaler) MarshalNaN(_ float64) error {
	return m.WriteString(".nan")
}

func (m *marshaler) MarshalInf(v float64) error {
	if v >= 0 {
		return m.WriteString(".inf")
	}
	return m.WriteString("-.inf")
}

func (m *marshaler) MarshalList(v reflect.Value) (bool, error) {
	if v.Len() == 0 {
		return true, m.WriteString("[]")
	}
	return false, nil
}

func (m *marshaler) MarshalListPost(v reflect.Value) error {
	return nil
}

func (m *marshaler) MarshalListElem(l, v reflect.Value, i int) (bool, error) {
	if err := m.WriteIndent(m.depth); err != nil {
		return false, err
	}
	if isCompound(v) {
		// Compound value: emit "-" then newline; children are indented one deeper.
		if err := m.WriteString("-"); err != nil {
			return false, err
		}
		if err := m.WriteNewline(); err != nil {
			return false, err
		}
		m.depth++
	} else {
		// Scalar value: emit "- " and the value follows inline on this line.
		if err := m.WriteString("- "); err != nil {
			return false, err
		}
	}
	return false, nil
}

func (m *marshaler) MarshalListElemPost(l, v reflect.Value, i int) error {
	if isCompound(v) {
		m.depth--
		// The compound value already ended with a newline from its last entry.
		return nil
	}
	return m.WriteNewline()
}

func (m *marshaler) Stringify(v reflect.Value) (string, bool, error) {
	return "", false, nil
}

func (m *marshaler) MarshalMap(v reflect.Value, kvs []reflectutil.MapEntry) (bool, error) {
	if len(kvs) == 0 {
		return true, m.WriteString("{}")
	}
	return false, nil
}

func (m *marshaler) MarshalMapPost(v reflect.Value, kvs []reflectutil.MapEntry) error {
	return nil
}

func (m *marshaler) MarshalMapKey(mv reflect.Value, kv reflectutil.MapEntry, i int) error {
	if err := m.WriteComment("# ", kv.Options.Help, m.depth); err != nil {
		return err
	}
	if err := m.WriteIndent(m.depth); err != nil {
		return err
	}
	return m.writeKey(kv.Key)
}

func (m *marshaler) MarshalMapValue(mv reflect.Value, kv reflectutil.MapEntry, i int) (bool, error) {
	if isCompound(kv.Value) {
		m.inlineVal = false
		if err := m.WriteString(":"); err != nil {
			return false, err
		}
		if err := m.WriteNewline(); err != nil {
			return false, err
		}
		m.depth++
		return false, nil
	}
	m.inlineVal = true
	return false, m.WriteString(": ")
}

func (m *marshaler) MarshalMapValuePost(mv reflect.Value, kv reflectutil.MapEntry, i int) error {
	if m.inlineVal {
		return m.WriteNewline()
	}
	m.depth--
	// The compound value should have ended with a newline; emit one only if it
	// didn't (e.g. MarshalMap/MarshalList returned ok=true for empty collections).
	if !m.tracker.lastWasNewline {
		return m.WriteNewline()
	}
	return nil
}

// MarshalNode and MarshalNodePost replay stored AST tokens verbatim, enabling
// round-trip encoding when Encode is passed a *syntax.Document.
func (m *marshaler) MarshalNode(node Value) error {
	for _, tok := range node.Base().Tokens {
		if err := m.WriteString(tok.Raw); err != nil {
			return err
		}
	}
	return nil
}

func (m *marshaler) MarshalNodePost(node Value) error {
	for _, tok := range node.Base().Suffix {
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
	_ reflectutil.NilMarshaler          = (*marshaler)(nil)
)

type EncoderOption func(*encoder)

func Marshal(v interface{}) ([]byte, error) {
	var out bytes.Buffer
	if err := NewEncoder(&out).Encode(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Save is a convenience function to save the value pointed at by v into a
// YAML document at path. It is functionally equivalent to NewEncoder(<file at path>).Encode(v).
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

// encodeDocument re-encodes a parsed YAML document verbatim by replaying all
// stored AST tokens. Used by tests; callers can equivalently call
// NewEncoder(w).Encode(doc).
func encodeDocument(w io.Writer, doc *Document) error {
	return NewEncoder(w).Encode(doc)
}
