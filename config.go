// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"snai.pe/boa/encoding"
	"snai.pe/boa/encoding/json5"
	"snai.pe/boa/encoding/toml"
)

// Decoders map filename extensions to decoders.  By default, the following
// coniguration languages are associated with the extensions:
//
//     - JSON5: .json and .json5
//     - TOML: .toml
//
// The contents of this map controls how the Decoder type deduces which
// decoder to use based on the file extension of the input.
var Decoders = map[string]func(io.Reader) encoding.Decoder {
	".toml":  toml.NewDecoder,
	".json5": json5.NewDecoder,
	".json":  json5.NewDecoder,
}

// Encoders map filename extensions to encoders.  By default, the following
// coniguration languages are associated with the extensions:
//
//     - JSON5: .json and .json5
//     - TOML: .toml
//
// The contents of this map controls how the Encoder type deduces which
// encoder to use based on the file extension of the output.
var Encoders = map[string]func(io.Writer) encoding.Encoder {
	".toml":  toml.NewEncoder,
	".json5": json5.NewEncoder,
	".json":  json5.NewEncoder,
}

// A Decoder reads and decodes a configuration from an input file.
type Decoder struct {
	in   encoding.StatableReader
	opts []interface{}
}

// NewDecoder returns a new Decoder that reads from `in`.
//
// The configuration language of the input file is deduced based on the
// file extension of its file path.  The file-extension-to-decoder mapping
// is defined by the contents of the Decoders map in this package.
//
// To only use one specific configuration language, do not use this decoder:
// Use instead the decoder for the chosen language in snai.pe/boa/encoding.
func NewDecoder(in encoding.StatableReader) *Decoder {
	return &Decoder{in: in, opts: append(([]interface{})(nil), defaultDecoderOptions...)}
}

func (dec *Decoder) Option(opts ...interface{}) encoding.Decoder {
	dec.opts = append(dec.opts, opts...)
	return dec
}

func (dec *Decoder) Decode(v interface{}) error {

	decode := func(in encoding.StatableReader) error {
		var name string
		// We need to determine the name of the input reader in order to
		// infer which decoder to use from the extension.
		info, err := in.Stat()
		if err != nil {
			// Some implementations of Stat() fail when the underlying file is
			// gone. Try to see if the reader implements Name() as a last resort.
			type Namer interface {
				Name() string
			}

			if namer, ok := in.(Namer); !ok {
				return err
			} else {
				name = namer.Name()
			}
		} else {
			name = info.Name()
		}
		ext := filepath.Ext(name)

		decoder, ok := Decoders[ext]
		if !ok {
			return fmt.Errorf("no known decoder for file extension %q", ext)
		}
		return decoder(in).Option(dec.opts...).Decode(v)
	}

	switch in := dec.in.(type) {
	case *FileSet:
		for {
			keys := make([]string, 0, len(Decoders))
			for k, _ := range Decoders {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			if err := in.Next(keys...); err != nil {
				if err == os.ErrNotExist {
					break
				}
				return err
			}
			f := in.File()
			err := decode(f)
			f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	default:
		return decode(in)
	}
}

// An Encoder encodes and writes a configuration into an output file.
type Encoder struct {
	out  encoding.StatableWriter
	opts []interface{}
}

// NewEncoder returns a new encoder that writes into `out`.
//
// The configuration language of the input file is deduced based on the
// file extension of its file path.  The file-extension-to-decoder mapping
// is defined by the contents of the Decoders map in this package.
//
// To only use one specific configuration language, do not use this encoder:
// Use instead the encoder for the chosen language in snai.pe/boa/encoding.
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

	encoder, ok := Encoders[ext]
	if !ok {
		return fmt.Errorf("no known decoder for file extension %q", ext)
	}
	return encoder(enc.out).Option(enc.opts...).Encode(v)
}

// Load loads the configuration files for the specified name into the specified
// value pointed to by v.
//
// The configuration language is deduced based on the file extension of the
// specified path:
//
//     - JSON5: .json and .json5
//     - TOML: .toml
//
// This is a convenience function that is functionally equivalent to:
//
// Custom file extensions are not supported, and one of the decoders in
// snai.pe/boa/encoding must be used instead.
func Load(name string, v interface{}) error {
	f := Open(name)
	defer f.Close()

	return NewDecoder(f).Decode(v)
}

// Save saves the specified value in v into a named configuration file.
//
// The name is interpreted relative to the return value of ConfigHome(). To
// save to arbitrary file paths, use os.Create and NewEncoder instead.
//
// The configuration language is deduced based on the file extension of the
// specified path:
//
//     - JSON5: .json and .json5
//     - TOML: .toml
//
// Custom file extensions are not supported, and one of the decoders in
// snai.pe/boa/encoding must be used instead.
func Save(name string, v interface{}) error {
	if filepath.IsAbs(name) {
		panic("Save does not take absolute paths; use os.Create and NewEncoder instead.")
	}
	if strings.HasPrefix(name, "."+string(filepath.Separator)) || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		panic("Save does not take cwd-relative paths; use os.Create and NewEncoder instead.")
	}

	configHome, err := ConfigHome()
	if err != nil {
		return err
	}
	path := filepath.Join(configHome, name)

	if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return NewEncoder(f).Encode(v)
}
