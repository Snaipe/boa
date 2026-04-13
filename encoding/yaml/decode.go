// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package yaml

import (
	"io"
	"os"
	"reflect"

	. "snai.pe/boa/syntax"

	"snai.pe/boa/encoding"
	"snai.pe/boa/internal/encutil"
	"snai.pe/boa/internal/reflectutil"
)

type structTagParser struct{}

func (structTagParser) ParseStructTag(tag reflect.StructTag) (reflectutil.FieldOpts, bool) {
	var opts reflectutil.FieldOpts
	if yamltag, ok := reflectutil.LookupTag(tag, "yaml", true); ok {
		if yamltag.Value == "-" {
			opts.Ignore = true
		} else {
			opts.Name = yamltag.Value
		}
		return opts, true
	}
	return opts, false
}

type unmarshaler struct {
	encutil.UnmarshalerBase
	structTagParser
	schema *Schema
}

func (*unmarshaler) UnmarshalValue(val reflect.Value, node Value) (bool, error) {
	return false, nil
}

var (
	_ reflectutil.StructTagParser = (*unmarshaler)(nil)
	_ reflectutil.Unmarshaler     = (*unmarshaler)(nil)
)

type decoder struct {
	in          io.Reader
	unmarshaler unmarshaler
}

func NewDecoder(rd io.Reader) encoding.Decoder {
	var decoder decoder
	decoder.in = rd
	decoder.unmarshaler.NewParser = newParser
	decoder.unmarshaler.Self = &decoder.unmarshaler
	decoder.unmarshaler.Extensions = []string{".yaml", ".yml"}

	// Defaults
	decoder.unmarshaler.NamingConvention = encoding.KebabCase
	decoder.unmarshaler.schema = YAML1_2
	return &decoder
}

func (decoder *decoder) Option(opts ...interface{}) encoding.Decoder {
	if err := decoder.unmarshaler.Option(opts...); err != nil {
		panic(err)
	}
	return decoder
}

func (decoder *decoder) Decode(v interface{}) error {
	return decoder.unmarshaler.Decode(decoder.in, v)
}

// Load is a convenience function to load a YAML document into the value
// pointed at by v. It is functionally equivalent to NewDecoder(<file at path>).Decode(v).
func Load(path string, v interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return NewDecoder(f).Decode(v)
}
