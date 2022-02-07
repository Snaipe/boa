// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package encoding

import (
	"encoding"
	"fmt"
	"io"
	"io/fs"

	"snai.pe/boa/syntax"
)

type Encoder interface {
	Encode(v interface{}) error
	Option(...interface{}) Encoder
}

type Decoder interface {
	Decode(v interface{}) error
	Option(...interface{}) Decoder
}

type LoadError struct {
	Filename string
	syntax.Cursor
	Target string
	Err    error
}

func (e *LoadError) Error() string {
	if e.Filename == "" {
		return fmt.Sprintf("at %d:%d: cannot load value into %v: %v", e.Line, e.Column, e.Target, e.Err)
	} else {
		return fmt.Sprintf("%s:%d:%d: cannot load value into %v: %v", e.Filename, e.Line, e.Column, e.Target, e.Err)
	}
}

func (e *LoadError) Unwrap() error {
	return e.Err
}

// CommonOptions represents the base set of configurations that may be set on
// any boa encoder or decoder.
//
// You should not be using this type directly -- instead, set relevant options
// via one of the options in the boa package, or package-specific options
// in the package of the relevant encoder or decoder.
type CommonOptions struct {
	NamingConvention NamingConvention
}

// CommonOption represents an option common to all encoders and decoders in boa.
type CommonOption func(*CommonOptions)

// EncoderOptions represents the base set of configurations that may be set on
// any boa encoder.
//
// You should not be using this type directly -- instead, set relevant options
// via one of the options in the boa package, or encoder-specific options
// in the package of the relevant encoder.
type EncoderOptions struct {
	Indent string
}

// EncoderOption represents an option common to all encoders in boa.
type EncoderOption func(*EncoderOptions)

// DecoderOptions represents the base set of configurations that may be set on
// any boa decoder.
//
// You should not be using this type directly -- instead, set relevant options
// via one of the options in the boa package, or decoder-specific options
// in the package of the relevant decoder.
type DecoderOptions struct {
	Indent       string
	AutomaticEnv bool
	EnvPrefix    string
	LookupEnv    func(string) (string, bool)
}

// DecoderOption represents an option common to all decoders in boa.
type DecoderOption func(*DecoderOptions)

// StatableReader is a reader that can be Stat()-ed.
type StatableReader interface {
	io.Reader
	Stat() (fs.FileInfo, error)
}

// StatableWriter is a writer that can be Stat()-ed.
type StatableWriter interface {
	io.Writer
	Stat() (fs.FileInfo, error)
}

// Alias some types from the standard encoding library for convenience

type (
	TextMarshaler   = encoding.TextMarshaler
	TextUnmarshaler = encoding.TextUnmarshaler
)
