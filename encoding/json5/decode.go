// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package json5

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"

	. "snai.pe/boa/syntax"

	"snai.pe/boa/encoding"
	"snai.pe/boa/internal/reflectutil"
)

type populator struct {
	structTagParser

	encoding.CommonOptions
	encoding.DecoderOptions
}

func (populator) PopulateValue(val reflect.Value, node *Node) (bool, error) {
	switch unmarshaler := val.Interface().(type) {
	case json.Unmarshaler:
		data, err := MarshalJSON(node)
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
	_ reflectutil.StructTagParser = (*populator)(nil)
	_ reflectutil.Populator       = (*populator)(nil)
)

type Decoder struct {
	parser    *parser
	populator populator
}

func NewDecoder(rd io.Reader) *Decoder {
	type namer interface {
		Name() string
	}
	name := ""
	if namer, ok := rd.(namer); ok {
		name = namer.Name()
	}
	decoder := Decoder{
		parser: newParser(name, rd),
	}
	return &decoder
}

func (decoder *Decoder) Option(opts ...interface{}) encoding.Decoder {
	for _, opt := range opts {
		switch setopt := opt.(type) {
		case encoding.CommonOption:
			setopt(&decoder.populator.CommonOptions)
		case encoding.DecoderOption:
			setopt(&decoder.populator.DecoderOptions)
		case DecoderOption:
			setopt(decoder)
		default:
			panic(fmt.Sprintf("%T is not a common option, nor an encoder option.", opt))
		}
	}
	return decoder
}

func (decoder *Decoder) Decode(v interface{}) error {
	root, err := decoder.parser.Parse()
	if err != nil {
		return err
	}
	if node, ok := v.(**Node); ok {
		*node = root
		return nil
	}
	ptr := reflect.ValueOf(v)
	if ptr.Kind() != reflect.Ptr {
		panic("json5.Decoder.Decode: must pass in pointer value")
	}

	convention := decoder.populator.NamingConvention
	if convention == nil {
		convention = encoding.CamelCase
	}

	err = reflectutil.Populate(ptr.Elem(), root.Child, convention, decoder.populator)
	if e, ok := err.(*encoding.LoadError); ok {
		e.Filename = decoder.parser.name
	}
	return err
}

type DecoderOption func(*Decoder)

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
