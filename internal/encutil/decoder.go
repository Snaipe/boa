// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package encutil

import (
	"fmt"
	"io"
	"io/fs"
	"reflect"

	"snai.pe/boa/encoding"
	"snai.pe/boa/internal/reflectutil"
	"snai.pe/boa/syntax"
)

type Unmarshaler interface {
	reflectutil.Unmarshaler
}

type DecoderBase struct {
	NewParser   func(io.Reader) syntax.Parser
	Unmarshaler Unmarshaler
}

func (decoder *DecoderBase) Decode(in io.Reader, v interface{}, convention encoding.NamingConvention) error {
	ptr := reflect.ValueOf(v)
	if ptr.Kind() != reflect.Ptr {
		panic("decode: must pass in pointer value")
	}

	root, err := decoder.NewParser(in).Parse()
	if err != nil {
		if e, ok := err.(*syntax.Error); ok {
			e.Filename = Name(in)
		}
		return err
	}
	if node, ok := v.(**syntax.Node); ok {
		*node = root
		return nil
	}
	err = reflectutil.Unmarshal(ptr.Elem(), root.Child, convention, decoder.Unmarshaler)
	if e, ok := err.(*encoding.LoadError); ok {
		e.Filename = Name(in)
	}
	return err
}

func (decoder *DecoderBase) Option(commonOpts *encoding.CommonOptions, decOpts *encoding.DecoderOptions, opts ...interface{}) error {
	for _, opt := range opts {
		switch setopt := opt.(type) {
		case encoding.CommonOption:
			setopt(commonOpts)
		case encoding.DecoderOption:
			setopt(decOpts)
		default:
			return fmt.Errorf("%T is not a common option, nor an decoder option.", opt)
		}
	}
	return nil
}

type Stater interface {
	Stat() (fs.FileInfo, error)
}

type Namer interface {
	Name() string
}

func Name(in interface{}) string {
	if namer, ok := in.(Namer); ok {
		return namer.Name()
	}
	if stater, ok := in.(Stater); ok {
		if info, err := stater.Stat(); err == nil {
			return info.Name()
		}
	}
	return ""
}
