// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package json5

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"snai.pe/boa/internal/reflectutil"
)

func noop(lhs, rhs reflect.Value) (bool, error) {
	return false, nil
}

func TestStandardSuite(t *testing.T) {
	filepath.Walk("testdata", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			t.Fatal(err)
		}

		name := filepath.Base(path)
		if name[0] == '.' {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		ext := filepath.Ext(name)
		switch ext {
		case ".json", ".json5", ".js", ".txt":
		default:
			return nil
		}

		t.Run(name[:len(name)-len(ext)], func(t *testing.T) {
			t.Parallel()

			txt, err := ioutil.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			switch ext {
			case ".json", ".json5":
				node, err := newParser(path, bytes.NewReader(txt)).Parse()
				if err != nil {
					t.Fatal(err)
				}

				var out strings.Builder
				if err := NewEncoder(&out).Encode(node); err != nil {
					t.Fatal(err)
				}

				if string(txt) != out.String() {
					abs, err := filepath.Abs(path)
					if err != nil {
						t.Fatal(err)
					}
					f, err := ioutil.TempFile("", "test-*"+ext)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if _, err := io.WriteString(f, out.String()); err != nil {
						t.Fatal(err)
					}
					t.Log("original:", abs)
					t.Log("re-encoded:", f.Name())
					t.Fatal("re-encoded configuration does not match original")
				}

				if ext == ".json5" {
					jf, err := os.Open(path[:len(path)-1])
					if errors.Is(err, os.ErrNotExist) {
						return
					}
					if err != nil {
						t.Fatal(err)
					}

					var first, second interface{}
					if err := NewDecoder(bytes.NewReader(txt)).Decode(&first); err != nil {
						t.Fatal(err)
					}
					if err := json.NewDecoder(jf).Decode(&second); err != nil {
						t.Fatal(err)
					}
					if err := reflectutil.DeepEqual(first, second, noop); err != nil {
						t.Fatal("decoded json5 is not equivalent to json:", err)
					}
				}

			case ".js", ".txt":
				node, err := newParser(path, bytes.NewReader(txt)).Parse()
				if err != nil {
					t.Log(err)
				} else {
					abs, err := filepath.Abs(path)
					if err != nil {
						t.Fatal(err)
					}
					t.Log("original:", abs)
					t.Log("parsed object:", node)
					t.Fatalf("expected decode of %v to fail", path)
				}
			}
		})

		return nil
	})
}

func BenchmarkStandardSuite(b *testing.B) {
	filepath.Walk("testdata", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			b.Fatal(err)
		}

		name := filepath.Base(path)
		if name[0] == '.' {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		ext := filepath.Ext(name)

		if ext != ".json" && ext != ".json5" {
			return nil
		}

		b.Run(name[:len(name)-len(ext)], func(b *testing.B) {
			txt, err := ioutil.ReadFile(path)
			if err != nil {
				b.Fatal(err)
			}

			for i := 0; i < b.N; i++ {
				_, err := newParser(path, bytes.NewReader(txt)).Parse()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
		return nil
	})
}
