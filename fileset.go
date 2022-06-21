// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileSet represents the combined set of configuration files with a specific
// name, that are contained within any of the directories in a search path.
type FileSet struct {
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
func Open(name string, paths ...fs.FS) *FileSet {
	if filepath.IsAbs(name) {
		panic("Open does not take absolute paths; use os.Open instead.")
	}
	if strings.HasPrefix(name, "."+string(filepath.Separator)) || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		panic("Open does not take cwd-relative paths; use os.Open instead.")
	}
	if paths == nil {
		paths = ConfigPaths()
	}
	return &FileSet{fs: paths, name: name}
}

// Read reads the contents of the currently opened configuration file into
// the data slice.
func (cfg *FileSet) Read(data []byte) (int, error) {
	if cfg.opened == nil {
		panic("FileSet.Next must be called first")
	}
	return cfg.opened.Read(data)
}

// Stat returns file information of the currently opened configuration file.
func (cfg *FileSet) Stat() (fs.FileInfo, error) {
	if cfg.opened == nil {
		panic("FileSet.Next must be called first")
	}
	return cfg.opened.Stat()
}

// Close closes the currently opened file.
func (cfg *FileSet) Close() error {
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
func (cfg *FileSet) Next(exts ...string) error {
	if cfg.opened != nil {
		// Close errors are ignored. FileSet only reads files, so close errors
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
	if cfg.opened == nil {
		// Nothing matched, but feed at the very least an empty file to the decoder.
		// This ensures that processes like automatic population from environment
		// variables still happen

		cfg.opened = discard
		cfg.index++
		return nil
	}
	return os.ErrNotExist
}

type discardFile struct {}

func (discardFile) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (discardFile) Close() error {
	return nil
}

func (discardFile) Stat() (fs.FileInfo, error) {
	return nil, errors.New("not implemented")
}

func (discardFile) Name() string {
	return "null"
}

var discard discardFile

// File returns the currently opened file.
func (cfg *FileSet) File() fs.File {
	if cfg.opened == nil {
		panic("FileSet.Next must be called first")
	}
	return cfg.opened
}

// Skip skips the current search path. This function is particularly useful when
// all of the matching configurations cannot be opened for any reason, and the
// application would rather warn but continue parsing configurations in the
// rest of the paths.
func (cfg *FileSet) Skip() {
	cfg.index++
}
