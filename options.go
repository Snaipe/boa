// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

import (
	"fmt"

	"snai.pe/boa/encoding"
)

// CommonOption represents an option common to all encoders and decoders in boa.
type CommonOption = encoding.CommonOption

// EncoderOption represents an option common to all encoders in boa.
type EncoderOption = encoding.EncoderOption

// DecoderOption represents an option common to all decoders in boa.
type DecoderOption = encoding.DecoderOption

// Indent returns an encoder option that sets the indentation to the specified
// whitespace string.
func Indent(indent string) EncoderOption {
	return func(opts *encoding.EncoderOptions) {
		opts.Indent = indent
	}
}

// NamingConvention returns an option that sets the default naming convention
// of an encoder or decoder to the specified convention.
//
// This option takes either a known convention name, or a naming convention
// value (as defined in the snai.pe/boa/encoding package)
//
// Supported naming conventions names are:
//
//     - "camelCase"
//     - "PascalCase"
//     - "snake_case"
//     - "SCREAMING_SNAKE_CASE"
//     - "kebab-case"
//     - "SCREAMING-KEBAB-CASE"
//     - "camel_Snake_Case"
//     - "Pascal_Snake_Case"
//     - "Train-Case"
//     - "flatcase"
//     - "UPPERFLATCASE"
//
func NamingConvention(name interface{}) CommonOption {
	var convention encoding.NamingConvention
	switch v := name.(type) {
	case string:
		convention = encoding.NamingConventionByName(v)
	case encoding.NamingConvention:
		convention = v
	default:
		panic(fmt.Sprintf("%T is neither a naming convention, or a naming convention name.", name))
	}

	return func(opts *encoding.CommonOptions) {
		opts.NamingConvention = convention
	}
}

var (
	defaultEncoderOptions []interface{}
	defaultDecoderOptions []interface{}
)

// SetDefaultOptions sets the default set of common, encoder-specific, and decoder-specific
// options for the functions in the boa package.
//
// Note that it does not change the defaults of specific encoding packages.
func SetDefaultOptions(options ...interface{}) {
	for _, opt := range options {
		switch opt.(type) {
		case CommonOption:
			defaultDecoderOptions = append(defaultDecoderOptions, opt)
			defaultEncoderOptions = append(defaultEncoderOptions, opt)
		case EncoderOption:
			defaultEncoderOptions = append(defaultEncoderOptions, opt)
		case DecoderOption:
			defaultDecoderOptions = append(defaultDecoderOptions, opt)
		default:
			panic(fmt.Sprintf("%T is not an option, encoder option or decoder option.", opt))
		}
	}
}
