// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package json5

import (
	"encoding/json"
	"io"
	"os"
	"reflect"

	. "snai.pe/boa/syntax"

	"snai.pe/boa/encoding"
	"snai.pe/boa/internal/encutil"
	"snai.pe/boa/internal/reflectutil"
)

type unmarshaler struct {
	structTagParser

	encoding.CommonOptions
	encoding.DecoderOptions
}

func (unmarshaler) UnmarshalValue(val reflect.Value, node *Node) (bool, error) {
	switch unmarshaler := val.Addr().Interface().(type) {
	case json.Unmarshaler:
		trimmed := node.Trim(punctAndWhitespace...)
		data, err := MarshalJSON(&trimmed)
		if err != nil {
			return false, err
		}
		if err := unmarshaler.UnmarshalJSON(data); err != nil {
			return false, err
		}
	default:
		return false, nil
	}
	return true, nil
}

var (
	_ reflectutil.StructTagParser = (*unmarshaler)(nil)
	_ reflectutil.Unmarshaler     = (*unmarshaler)(nil)
)

type decoder struct {
	in          io.Reader
	base        encutil.DecoderBase
	unmarshaler unmarshaler
}

func NewDecoder(rd io.Reader) encoding.Decoder {
	var decoder decoder
	decoder.in = rd
	decoder.base.NewParser = newParser
	decoder.base.Unmarshaler = &decoder.unmarshaler
	return &decoder
}

func (decoder *decoder) Option(opts ...interface{}) encoding.Decoder {
	err := decoder.base.Option(&decoder.unmarshaler.CommonOptions, &decoder.unmarshaler.DecoderOptions, opts...)
	if err != nil {
		panic(err)
	}
	return decoder
}

func (decoder *decoder) Decode(v interface{}) error {
	convention := decoder.unmarshaler.NamingConvention
	if convention == nil {
		convention = encoding.CamelCase
	}
	return decoder.base.Decode(decoder.in, v, convention)
}

// Load is a convenience function to load a JSON5 document into the value
// pointed at by v. It is functionally equivalent to NewDecoder(<file at path>).Decode(v).
func Load(path string, v interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return NewDecoder(f).Decode(v)
}
