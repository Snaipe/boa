// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"snai.pe/boa/encoding"
	"snai.pe/boa/encoding/json5"
	"snai.pe/boa/encoding/toml"
)

// ConfigFile represents the combined set of configuration files with a specific
// name, that are contained within any of the directories in a search path.
type ConfigFile struct {
	fs     []fs.FS
	name   string
	index  int
	opened fs.File
	closed bool
}

// Open opens a set of configuration files by name.
//
// The files are searched by matching the stem (i.e. the filename without
// extension) of the files in the provided search paths to the specified name.
// If no path is provided, the result of ConfigPaths() is used.
//
// The provided name can be a filename with an extension, or without an extension.
// It can also include one or more directory components, but must not be an
// absolute filesystem path; use os.Open instead to load configuration from
// specific filesystem paths.
//
// Names with an extension will restrict the search to the files matching the
// specified extension. Conversely, names without an extension will match all
// files whose stem is the last path component of the filename.
func Open(name string, paths ...fs.FS) *ConfigFile {
	if filepath.IsAbs(name) {
		panic("Open does not take absolute paths; use os.Open instead.")
	}
	if strings.HasPrefix(name, "."+string(filepath.Separator)) || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		panic("Open does not take cwd-relative paths; use os.Open instead.")
	}
	if paths == nil {
		paths = ConfigPaths()
	}
	return &ConfigFile{fs: paths, name: name}
}

// Read reads the contents of the currently opened configuration file into
// the data slice.
func (cfg *ConfigFile) Read(data []byte) (int, error) {
	if cfg.opened == nil {
		panic("ConfigFile.Next must be called first")
	}
	return cfg.opened.Read(data)
}

// Stat returns file information of the currently opened configuration file.
func (cfg *ConfigFile) Stat() (fs.FileInfo, error) {
	if cfg.opened == nil {
		panic("ConfigFile.Next must be called first")
	}
	return cfg.opened.Stat()
}

// Close closes the currently opened file.
func (cfg *ConfigFile) Close() error {
	if cfg.opened == nil {
		return nil
	}
	return cfg.opened.Close()
}

// Next closes the current configuration file if any is opened, and opens
// the first file in the next path that matches the set of allowed file extensions.
//
// All subsequent calls to Read, Stat, and Close will then apply on the newly
// opened configuration file.
//
// If all of the search paths have been exhausted, os.ErrNotExist is returned.
//
// The files are matched in the order of the specified exts slice rather than
// directory (or lexical) order. For insteance, if the extension slice
// is ".json5", ".json" on a path containing <name>.json5 and <name>.json will
// open <name>.json5 if it exists, or then <name>.json.
func (cfg *ConfigFile) Next(exts ...string) error {
	if cfg.opened != nil {
		// Close errors are ignored. ConfigFile only reads files, so close errors
		// do not matter.
		_ = cfg.opened.Close()
	}

	for ; cfg.index < len(cfg.fs); cfg.index++ {
		for _, ext := range exts {
			realext := filepath.Ext(cfg.name)
			if realext != "" && realext != ext {
				continue
			}
			stem := cfg.name[:len(cfg.name)-len(realext)]
			f, err := cfg.fs[cfg.index].Open(stem + ext)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				continue
			case err != nil:
				return err
			}
			cfg.opened = f
			cfg.index++
			return nil
		}
	}
	return os.ErrNotExist
}

// File returns the currently opened file.
func (cfg *ConfigFile) File() fs.File {
	if cfg.opened == nil {
		panic("ConfigFile.Next must be called first")
	}
	return cfg.opened
}

// Skip skips the current search path. This function is particularly useful when
// all of the matching configurations cannot be opened for any reason, and the
// application would rather warn but continue parsing configurations in the
// rest of the paths.
func (cfg *ConfigFile) Skip() {
	cfg.index++
}

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

		var decoder encoding.Decoder
		switch ext {
		case ".json", ".json5":
			decoder = json5.NewDecoder(in)
		case ".toml":
			decoder = toml.NewDecoder(in)
		default:
			return fmt.Errorf("no known decoder for file extension %q", ext)
		}
		return decoder.Option(dec.opts...).Decode(v)
	}

	switch in := dec.in.(type) {
	case *ConfigFile:
		for {
			if err := in.Next(".toml", ".json5", ".json"); err != nil {
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
