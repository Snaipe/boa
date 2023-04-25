// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileSet represents the combined set of configuration files with a specific
// name, that are contained within any of the directories in a search path.
type FileSet struct {
	fs        []fs.FS
	fsIndex   int
	names     []string
	nameIndex int
	opened    fs.File
	closed    bool
	used      []string
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
	checkNames(name)
	if paths == nil {
		paths = ConfigPaths()
	}
	return &FileSet{fs: paths, names: []string{name}}
}

// OpenMultiple opens a set of configuration files with one or more names.
//
// The order of the `names` slice defines a precedence of files across all
// search paths. With a single `name`, the behaviour is identical to `Open`.
//
// The iteration order is described in detail in `Next()`.
func OpenMultiple(names []string, paths ...fs.FS) *FileSet {
	checkNames(names...)
	if paths == nil {
		paths = ConfigPaths()
	}
	return &FileSet{fs: paths, names: names}
}

func checkNames(names ...string) {
	for _, name := range names {
		if filepath.IsAbs(name) {
			panic("Open does not take absolute paths; use os.Open instead.")
		}
		if strings.HasPrefix(name, "."+string(filepath.Separator)) || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			panic("Open does not take cwd-relative paths; use os.Open instead.")
		}
	}
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
	if err := cfg.opened.Close(); err != nil {
		return err
	}
	cfg.opened = nil
	return nil
}

func fsPath(f fs.FS) string {
	switch f.(type) {
	case embed.FS:
		return "(embedded)"
	default:
		return fmt.Sprint(f)
	}
}

// Next closes the current configuration file if any is opened, and opens
// the first file in the next path that matches the set of allowed file extensions.
//
// All subsequent calls to Read, Stat, and Close will then apply on the newly
// opened configuration file.
//
// If all of the search paths have been exhausted, os.ErrNotExist is returned.
//
// When there are multiple names (see OpenMultiple), they will be returned in order
// first by name and then by path. For instance, if the names slice is "base",
// "override" and the paths slice is "/etc", "/root/.config" we'll use the ordering:
//
//   - /etc/base.<ext>
//   - /root/.config/base.<ext>
//   - /etc/override.<ext>
//   - /root/.config/override.<ext>
//
// This ordering of the names slice ensures that later entries take precedence over
// earlier ones, regardless of which directory they're in.
//
// The files are matched in the order of the specified exts slice rather than
// directory (or lexical) order. For instance, if the extension slice
// is ".json5", ".json" on a path containing <name>.json5 and <name>.json will
// open <name>.json5 if it exists, or then <name>.json.
func (cfg *FileSet) Next(exts ...string) error {
	if cfg.opened != nil {
		// Close errors are ignored. FileSet only reads files, so close errors
		// do not matter.
		_ = cfg.opened.Close()
	}

	for ; cfg.nameIndex < len(cfg.names); cfg.nameIndex++ {
		for ; cfg.fsIndex < len(cfg.fs); cfg.fsIndex++ {
			for _, ext := range exts {
				name := cfg.names[cfg.nameIndex]
				realext := filepath.Ext(name)
				if realext != "" && realext != ext {
					continue
				}
				stem := name[:len(name)-len(realext)]
				f, err := cfg.fs[cfg.fsIndex].Open(stem + ext)
				switch {
				case errors.Is(err, fs.ErrNotExist):
					continue
				case err != nil:
					return err
				}
				cfg.used = append(cfg.used, fmt.Sprintf("%v/%v%v", fsPath(cfg.fs[cfg.fsIndex]), stem, ext))
				cfg.opened = f
				cfg.Skip()
				return nil
			}
		}
	}
	if cfg.opened == nil {
		// Nothing matched, but feed at the very least an empty file to the decoder.
		// This ensures that processes like automatic population from environment
		// variables still happen

		cfg.opened = discard
		cfg.fsIndex++
		return nil
	}
	return os.ErrNotExist
}

// Used returns a slice containing the file names of all files that were opened by
// calls to Next().
func (cfg *FileSet) Used() []string {
	v := make([]string, len(cfg.used))
	copy(v, cfg.used)
	return v
}

type discardFile struct{}

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
	cfg.fsIndex++
	if cfg.fsIndex == len(cfg.fs) {
		cfg.fsIndex = 0
		cfg.nameIndex++
	}
}
