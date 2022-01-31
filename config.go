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

	ext := filepath.Ext(path)

	var decoder encoding.Decoder
	switch ext {
	case ".json", ".json5":
		decoder = json5.NewDecoder(f)
	case ".toml":
		decoder = toml.NewDecoder(f)
	default:
		return fmt.Errorf("no known decoder for file extension %q", ext)
	}
	decoder = decoder.Option(defaultDecoderOptions...)

	return decoder.Decode(v)
}

func Save(path string, v interface{}) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	ext := filepath.Ext(path)

	var encoder encoding.Encoder
	switch ext {
	case ".json", ".json5":
		encoder = json5.NewEncoder(f)
	case ".toml":
		encoder = toml.NewEncoder(f)
	default:
		return fmt.Errorf("no known encoder for file extension %q", ext)
	}
	encoder = encoder.Option(defaultEncoderOptions...)

	return encoder.Encode(v)
}
