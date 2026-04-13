// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package yaml

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"snai.pe/boa/encoding"
	"snai.pe/boa/encoding/json5"
	"snai.pe/boa/internal/reflectutil"
	"snai.pe/boa/syntax"
)

func DeepEqual(lhs, rhs interface{}) error {
	if lhs == nil && rhs == nil {
		return nil
	}
	if lhs == nil || rhs == nil {
		return fmt.Errorf("one value is nil while the other is not")
	}
	return reflectutil.DeepEqual(lhs, rhs, func(lv, rv reflect.Value) (bool, error) {
		// Allow numeric comparisons across int64/uint64/float64 types,
		// since YAML may parse "450.00" as float64 while JSON parses "450" as int64.
		lk, rk := lv.Kind(), rv.Kind()
		lIsNum := lk == reflect.Int64 || lk == reflect.Uint64 || lk == reflect.Float64
		rIsNum := rk == reflect.Int64 || rk == reflect.Uint64 || rk == reflect.Float64
		if lIsNum && rIsNum && lk != rk {
			var lf, rf float64
			switch lk {
			case reflect.Int64:
				lf = float64(lv.Int())
			case reflect.Uint64:
				lf = float64(lv.Uint())
			case reflect.Float64:
				lf = lv.Float()
			}
			switch rk {
			case reflect.Int64:
				rf = float64(rv.Int())
			case reflect.Uint64:
				rf = float64(rv.Uint())
			case reflect.Float64:
				rf = rv.Float()
			}
			if lf == rf {
				return true, nil
			}
			return true, fmt.Errorf("values not equal")
		}
		return false, nil
	})
}

func abs(components ...string) string {
	abs, err := filepath.Abs(filepath.Join(components...))
	if err != nil {
		panic(err)
	}
	return abs
}

// runYAMLParserSuite walks dir and for each named test case (directories
// containing a "===" name file):
//  1. Parses in.yaml using mkParser; expects a parse error if the "error"
//     marker file exists.
//  2. Re-encodes each document through the public encoder and verifies the
//     output is byte-for-byte identical to the original (round-trip).
//  3. If in.json is present, decodes the parsed documents into interface{}
//     values and compares them against the JSON reference (semantic check).
//
// skip is a list of directory name suffixes to skip (e.g. known-bad cases).
func runYAMLParserSuite(t *testing.T, dir string, mkParser func(io.Reader) syntax.Parser, skip []string) {
	t.Helper()
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			t.Fatal(err)
		}

		if !info.IsDir() {
			return nil
		}
		if info.Name()[0] == '.' {
			return filepath.SkipDir
		}

		name, err := os.ReadFile(filepath.Join(path, "==="))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}

		t.Run(strings.TrimSpace(string(name)), func(t *testing.T) {
			t.Log("original:", abs(path, "in.yaml"))

			original, err := os.ReadFile(filepath.Join(path, "in.yaml"))
			if err != nil {
				t.Fatal(err)
			}

			var expectErr bool
			if _, err := os.Stat(filepath.Join(path, "error")); err == nil {
				expectErr = true
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}

			if expectErr {
				// Iterate all documents in the stream; an error anywhere counts.
				p := mkParser(bytes.NewReader(original))
				var gotErr bool
				for {
					_, err := p.Parse()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						gotErr = true
						break
					}
				}
				if !gotErr {
					t.Fatal("expected error")
				}
				return
			}

			// Parse all documents from the original file.
			p := mkParser(bytes.NewReader(original))
			var docs []*syntax.Document
			for {
				doc, err := p.Parse()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("parse error: %v", err)
				}
				docs = append(docs, doc)
			}

			// Round-trip: re-encode all documents through the public encoder and
			// verify the output matches the original byte-for-byte.
			var encoded bytes.Buffer
			enc := NewEncoder(&encoded)
			for _, doc := range docs {
				if err := enc.Encode(doc); err != nil {
					t.Fatalf("encode error: %v", err)
				}
			}
			got := encoded.Bytes()
			if !bytes.Equal(got, original) {
				t.Logf("original (%d bytes):\n%s", len(original), original)
				t.Logf("encoded  (%d bytes):\n%s", len(got), got)
				for i := 0; i < len(original) && i < len(got); i++ {
					if original[i] != got[i] {
						t.Logf("first diff at byte %d: original=%q encoded=%q", i, original[i], got[i])
						break
					}
				}
				t.Error("round-trip encoding differs from original")
			}

			// Semantic check: if in.json is present, decode documents into
			// interface{} and compare against the JSON reference values.
			expFile, err := os.Open(filepath.Join(path, "in.json"))
			if errors.Is(err, os.ErrNotExist) {
				return // no JSON reference; round-trip check above is sufficient
			}
			if err != nil {
				t.Fatal(err)
			}
			defer expFile.Close()

			for _, s := range skip {
				if strings.HasSuffix(path, s) {
					t.Skipf("skipped: %s", s)
					return
				}
			}

			// Read all expected JSON documents, re-decoding each through json5 to
			// get types matching what the YAML decoder produces.
			var expectedDocs []interface{}
			jsonDec := stdjson.NewDecoder(expFile)
			for {
				var raw stdjson.RawMessage
				if err := jsonDec.Decode(&raw); err == io.EOF {
					if len(expectedDocs) == 0 {
						expectedDocs = append(expectedDocs, nil)
					}
					break
				} else if err != nil {
					t.Fatal(err)
				}
				var v interface{}
				if err := json5.NewDecoder(strings.NewReader(string(raw))).Decode(&v); err != nil {
					t.Fatal(err)
				}
				expectedDocs = append(expectedDocs, v)
			}

			// Decode each parsed document into interface{} and compare.
			var actualDocs []interface{}
			for _, doc := range docs {
				var v interface{}
				if err := reflectutil.Unmarshal(reflect.ValueOf(&v).Elem(), doc.Root, encoding.SnakeCase, true, nil); err != nil {
					t.Fatal(err)
				}
				actualDocs = append(actualDocs, v)
			}

			if len(actualDocs) != len(expectedDocs) {
				t.Logf("actual: %v", actualDocs)
				t.Logf("expected: %v", expectedDocs)
				t.Fatalf("expected %d document(s) but got %d", len(expectedDocs), len(actualDocs))
				return
			}

			for i := range expectedDocs {
				if err := DeepEqual(actualDocs[i], expectedDocs[i]); err != nil {
					if len(expectedDocs) == 1 {
						t.Logf("actual: %v", actualDocs[i])
						t.Logf("expected: %v", expectedDocs[i])
						t.Fatal(err)
					} else {
						t.Logf("actual[%d]: %v", i, actualDocs[i])
						t.Logf("expected[%d]: %v", i, expectedDocs[i])
						t.Fatalf("doc %d: %v", i, err)
					}
				}
			}
		})

		return filepath.SkipDir
	})
}

// TestYAMLParser walks the standard YAML test suite and testdata/yaml1.2,
// both using the YAML 1.2 core schema.
func TestYAMLParser(t *testing.T) {
	mkParser := func(r io.Reader) syntax.Parser { return newParser(r) }
	// Some standard test cases contain invalid JSON in the reference file.
	runYAMLParserSuite(t, "testdata/standard", mkParser, []string{"8G76", "HWV9"})
	runYAMLParserSuite(t, "testdata/yaml1.2", mkParser, nil)
}

// TestYAML11Parser walks testdata/yaml1.1 using the YAML 1.1 schema, which
// accepts the broader boolean forms (y/n, yes/no, on/off) and the legacy
// octal (0NNN) and binary (0bNNN) integer prefixes.
func TestYAML11Parser(t *testing.T) {
	mkParser := func(r io.Reader) syntax.Parser {
		p := newParser(r).(*parser)
		p.schema = YAML1_1
		return p
	}
	runYAMLParserSuite(t, "testdata/yaml1.1", mkParser, nil)
}
