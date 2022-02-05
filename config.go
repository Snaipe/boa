// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

import (
	"fmt"
	"os"
	"path/filepath"

	"snai.pe/boa/encoding"
	"snai.pe/boa/encoding/json5"
	"snai.pe/boa/encoding/toml"
)

// A Decoder reads and decodes a configuration from an input file.
type Decoder struct {
	in   encoding.StatableReader
	opts []interface{}
}

// NewDecoder returns a new Decoder that reads from `in`.
//
// The configuration language of the input file is deduced based on the
// file extension of its file path:
//
//     - JSON5: .json and .json5
//     - TOML: .toml
//
// Custom file extensions are not supported, and one of the decoders in
// snai.pe/boa/encoding must be used instead.
func NewDecoder(in encoding.StatableReader) *Decoder {
	return &Decoder{in: in, opts: append(([]interface{})(nil), defaultDecoderOptions...)}
}

func (dec *Decoder) Option(opts ...interface{}) encoding.Decoder {
	dec.opts = append(dec.opts, opts...)
	return dec
}

func (dec *Decoder) Decode(v interface{}) error {
	var name string
	// We need to determine the name of the input reader in order to
	// infer which decoder to use from the extension.
	info, err := dec.in.Stat()
	if err != nil {
		// Some implementations of Stat() fail when the underlying file is
		// gone. Try to see if the reader implements Name() as a last resort.
		type Namer interface {
			Name() string
		}

		if namer, ok := dec.in.(Namer); !ok {
			return err
		} else {
			name = namer.Name()
		}
	} else {
		name = info.Name()
	}
	ext := filepath.Ext(name)

	var decoder encoding.Decoder
	switch ext {
	case ".json", ".json5":
		decoder = json5.NewDecoder(dec.in)
	case ".toml":
		decoder = toml.NewDecoder(dec.in)
	default:
		return fmt.Errorf("no known decoder for file extension %q", ext)
	}
	return decoder.Option(dec.opts...).Decode(v)
}

// An Encoder encodes and writes a configuration into an output file.
type Encoder struct {
	out  encoding.StatableWriter
	opts []interface{}
}

// NewEncoder returns a new encoder that writes into `out`.
//
// The configuration language of the output file is deduced based on the
// file extension of its file path:
//
//     - JSON5: .json and .json5
//     - TOML: .toml
//
// Custom file extensions are not supported, and one of the decoders in
// snai.pe/boa/encoding must be used instead.
func NewEncoder(out encoding.StatableWriter) *Encoder {
	return &Encoder{out: out, opts: append(([]interface{})(nil), defaultEncoderOptions...)}
}

func (enc *Encoder) Option(opts ...interface{}) encoding.Encoder {
	enc.opts = append(enc.opts, opts...)
	return enc
}

func (enc *Encoder) Encode(v interface{}) error {
	var name string
	// We need to determine the name of the input reader in order to
	// infer which decoder to use from the extension.
	info, err := enc.out.Stat()
	if err != nil {
		// Some implementations of Stat() fail when the underlying file is
		// gone. Try to see if the reader implements Name() as a last resort.
		type Namer interface {
			Name() string
		}

		if namer, ok := enc.out.(Namer); !ok {
			return err
		} else {
			name = namer.Name()
		}
	} else {
		name = info.Name()
	}
	ext := filepath.Ext(name)

	var encoder encoding.Encoder
	switch ext {
	case ".json", ".json5":
		encoder = json5.NewEncoder(enc.out)
	case ".toml":
		encoder = toml.NewEncoder(enc.out)
	default:
		return fmt.Errorf("no known decoder for file extension %q", ext)
	}
	return encoder.Option(enc.opts...).Encode(v)
}

// Load loads the configuration file at path into the specified value pointed
// to by v.
//
// The configuration language is deduced based on the file extension of the
// specified path:
//
//     - JSON5: .json and .json5
//     - TOML: .toml
//
// Custom file extensions are not supported, and one of the decoders in
// snai.pe/boa/encoding must be used instead.
func Load(path string, v interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return NewDecoder(f).Decode(v)
}

// Save saves the specified value in v into the configuration file at path.
//
// The configuration language is deduced based on the file extension of the
// specified path:
//
//     - JSON5: .json and .json5
//     - TOML: .toml
//
// Custom file extensions are not supported, and one of the decoders in
// snai.pe/boa/encoding must be used instead.
func Save(path string, v interface{}) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return NewEncoder(f).Encode(v)
}
