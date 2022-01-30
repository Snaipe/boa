// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package encoding

import (
	"encoding"
	"fmt"

	"snai.pe/boa/syntax"
)

type Encoder interface {
	Encode(v interface{}) error
}

type Decoder interface {
	Decode(v interface{}) error
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

// Alias some types from the standard encoding library for convenience

type (
	TextMarshaler   = encoding.TextMarshaler
	TextUnmarshaler = encoding.TextUnmarshaler
)
