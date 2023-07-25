// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"testing"
)

func pathsForDirs(t *testing.T, dirs ...string) (paths []fs.FS) {
	t.Helper()
	dirname, err := os.Getwd()
	if err != nil {
		t.Fatal("Getwd:", err)
	}

	for _, d := range dirs {
		paths = append(paths, os.DirFS(filepath.Join(dirname, "testdata/fileset", d)))
	}
	return paths
}

func keepLastPathComponents(names []string) (out []string) {
	for _, n := range names {
		out = append(out, filepath.Join(filepath.Base(filepath.Dir(n)), filepath.Base(n)))
	}
	return out
}

func assertUsedEqual(t *testing.T, first, second []string) {
	t.Helper()
	firstStr := fmt.Sprint(keepLastPathComponents(first))
	secondStr := fmt.Sprint(keepLastPathComponents(second))
	if firstStr != secondStr {
		t.Errorf("mismatch in assertUsedEqual:\n"+
			"A: %s\n"+
			"B: %s", firstStr, secondStr)
	}
}

func TestFileset(t *testing.T) {
	type testCase struct {
		name       string
		open       func(t *testing.T, c *testCase) *FileSet
		expectUsed []string
	}
	for _, tc := range []testCase{
		{
			name: "single-path",
			open: func(t *testing.T, c *testCase) *FileSet {
				return Open("first", pathsForDirs(t, "path1")...)
			},
			expectUsed: []string{"path1/first.json"},
		},
		{
			name: "multi-path",
			open: func(t *testing.T, c *testCase) *FileSet {
				return Open("first", pathsForDirs(t, "path1", "path2")...)
			},
			expectUsed: []string{"path1/first.json", "path2/first.toml"},
		},
		{
			name: "multi-name-multi-path",
			open: func(t *testing.T, c *testCase) *FileSet {
				return OpenMultiple([]string{"first", "second"}, pathsForDirs(t, "path1", "path2")...)
			},
			expectUsed: []string{
				"path1/first.json", "path2/first.toml",
				"path1/second.toml", "path2/second.json",
			},
		},
		{
			name: "empty-first",
			open: func(t *testing.T, c *testCase) *FileSet {
				return OpenMultiple(
					[]string{"first", "second"},
					pathsForDirs(t, "empty1", "path1")...,
				)
			},
			expectUsed: []string{"path1/first.json", "path1/second.toml"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exts := []string{".toml", ".json", ".json5"}
			cfg := tc.open(t, &tc)
			if cfg.opened != nil {
				t.Fatal("cfg.opened should be nil")
			}
			defer cfg.Close()
			for {
				if err := cfg.Next(exts...); err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						break
					}
					t.Fatal("next:", err)
				}
				if cfg.opened == nil {
					t.Fatal("cfg.opened should be non nil")
				}
			}
			assertUsedEqual(t, tc.expectUsed, cfg.Used())
		})
	}
}

func ExampleSetConfigHomeFS() {

	var config struct {
		Greeting string `help:"A nice hello."`
	}

	// Use NewSingleFileFS to expose example_defaults.toml as program.toml.
	//
	// This could be used to override the default config paths via the
	// environment or a flag
	SetConfigHomeFS(
		NewSingleFileFS("program.toml", "example_defaults.toml"),
	)

	cfg := Open("program.toml")
	defer cfg.Close()

	// Load the defaults into the config variable
	if err := NewDecoder(cfg).Decode(&config); err != nil {
		log.Fatalln(err)
	}

	fmt.Println(config.Greeting)
	// Output: Hello from TOML!
}
