// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

import (
	"fmt"
	"strings"

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

// LineBreak sets the line break sequence that must be used when encoding
// documents.
//
// Can either be "\n" or "\r\n".
func LineBreak(lb string) EncoderOption {
	if lb != "\n" && lb != "\r\n" {
		panic("line break must either be \\n or \\r\\n.")
	}
	return func(opts *encoding.EncoderOptions) {
		opts.LineBreak = lb
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

// AutomaticEnv enables the automatic population of config values from the
// environment. An optional prefix may be specified for the environment
// variable names.
func AutomaticEnv(prefix string) DecoderOption {
	return func(opts *encoding.DecoderOptions) {
		opts.AutomaticEnv = true
		opts.EnvPrefix = prefix
	}
}

// Environ sets the environment variables that will be used for any
// substitution, either by fields marked with an `env` tag, or fields
// implicitly matching variables via AutomaticEnv.
//
// Incompatible with EnvironFunc. Setting Environ after EnvironFunc
// overrides the lookup function that EnvironFunc previously set.
func Environ(env []string) DecoderOption {
	environ := make(map[string]string, len(env))
	for _, e := range env {
		split := strings.SplitN(e, "=", 2)
		if len(split) != 2 {
			panic("env has `" + e + "`, which is not in key=value form.")
		}
		environ[split[0]] = split[1]
	}

	lookupEnv := func(k string) (string, bool) {
		v, ok := environ[k]
		return v, ok
	}

	return EnvironFunc(lookupEnv)
}

// EnvironFunc sets the lookup function for environment variables. By default,
// os.LookupEnv is used.
//
// Incompatible with EnvironFunc. Setting EnvironFunc after Environ
// overrides the variables that Environ previously set.
func EnvironFunc(fn func(string) (string, bool)) DecoderOption {
	return func(opts *encoding.DecoderOptions) {
		opts.LookupEnv = fn
	}
}

var (
	defaultEncoderOptions []interface{}
	defaultDecoderOptions []interface{}
)

// SetOptions sets the default set of common, encoder-specific, and decoder-specific
// options for the functions in the boa package.
//
// Note that it does not change the defaults of specific encoding packages.
func SetOptions(options ...interface{}) {
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
