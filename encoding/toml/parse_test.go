// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package toml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/constant"
	"go/token"
	"io/ioutil"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"snai.pe/boa/internal/reflectutil"
)

func DeepEqual(lhs, rhs interface{}) error {

	normalize := func(val reflect.Value) *time.Time {
		switch t := val.Interface().(type) {
		case LocalDateTime:
			res := t.Time(time.UTC)
			return &res
		case LocalDate:
			res := t.Time(0, 0, 0, 0, time.UTC)
			return &res
		case LocalTime:
			res := t.Time(0, 1, 1, time.UTC)
			return &res
		case time.Time:
			return &t
		}
		return nil
	}

	return reflectutil.DeepEqual(lhs, rhs, func(lhs, rhs reflect.Value) (bool, error) {
		normlhs := normalize(lhs)
		normrhs := normalize(rhs)
		if normlhs != nil && normrhs != nil {
			if !normlhs.Equal(*normrhs) {
				return false, fmt.Errorf("%v does not equal %v", *normlhs, *normrhs)
			}
			return true, nil
		}
		return false, nil
	})
}

type TomlTest struct {
	value interface{}
}

func (t *TomlTest) UnmarshalJSON(data []byte) error {
	type value struct {
		Type  string
		Value string
	}

	var val value
	if err := json.Unmarshal(data, &val); err == nil && val.Type != "" {
		switch val.Type {
		case "string":
			t.value = val.Value
		case "integer":
			constv := constant.MakeFromLiteral(val.Value, token.INT, 0)
			t.value = reflectutil.ConstantToInt(constv).Interface()
		case "float":
			if val.Value == "nan" {
				t.value = math.NaN()
			} else {
				const prec = 512 // matches lexer
				var flt *big.Float
				flt, _, err = big.ParseFloat(val.Value, 0, prec, big.ToNearestEven)
				if err != nil {
					return err
				}
				if flt.IsInf() {
					t.value, _ = flt.Float64()
				} else {
					t.value = reflectutil.ConstantToFloat(constant.Make(flt)).Interface()
				}
			}
		case "bool":
			t.value, err = strconv.ParseBool(val.Value)
		case "datetime":
			t.value, err = time.Parse("2006-01-02T15:04:05.999999999Z07:00", val.Value)
		case "datetime-local":
			t.value, err = time.Parse("2006-01-02T15:04:05.999999999", val.Value)
		case "date-local":
			t.value, err = time.Parse("2006-01-02", val.Value)
		case "time-local":
			t.value, err = time.Parse("15:04:05", val.Value)
		}
		return err
	}

	var valmap map[string]TomlTest
	if err := json.Unmarshal(data, &valmap); err == nil {
		out := make(map[string]interface{}, len(valmap))
		for k, v := range valmap {
			out[k] = v.value
		}
		t.value = out
		return nil
	}

	var vallist []TomlTest
	if err := json.Unmarshal(data, &vallist); err == nil {
		out := make([]interface{}, len(vallist))
		for i, v := range vallist {
			out[i] = v.value
		}
		t.value = out
		return nil
	}

	return errors.New("toml test is neither a value, an object, or a list")
}

func TestTOMLStandardSuite(t *testing.T) {
	filepath.Walk("testdata/tests", func(path string, info os.FileInfo, err error) error {
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
		if ext != ".toml" {
			return nil
		}

		testname := path[:len(path)-len(ext)][len("testdata/tests/"):]
		jsonpath := path[:len(path)-len(ext)] + ".json"

		abspath, err := filepath.Abs(path)
		if err != nil {
			return err
		}

		jsonabspath, err := filepath.Abs(jsonpath)
		if err != nil {
			t.Fatal(err)
		}

		txt, err := ioutil.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		var expected TomlTest
		if f, err := os.Open(jsonpath); err == nil {
			err := json.NewDecoder(f).Decode(&expected)
			f.Close()
			if err != nil {
				t.Fatal("cannot parse valid toml test case:", err)
			}
		}

		t.Run(testname + "/decode", func(t *testing.T) {
			t.Parallel()

			var actual interface{}
			err = NewDecoder(bytes.NewReader(txt)).Decode(&actual)

			t.Log("original:", abspath)
			if expected.value == nil {
				// this is an invalid test case, expect an error
				t.Log("error:", err)
				if err == nil {
					t.Log("parsed object:", actual)
					t.Fatal("expected an error, but did not get one")
				}
			} else {
				t.Log("testcase:", jsonabspath)
				if err != nil {
					t.Fatal(err)
				}
				if err := DeepEqual(actual, expected.value); err != nil {
					t.Log("actual:", actual)
					t.Log("expected:", expected.value)
					t.Fatal(err)
				}
			}
		})

		if expected.value == nil {
			// Do not run encode & re-encode tests for invalid toml cases
			return nil
		}

		showContext := func(out []byte) {
			t.Helper()

			abs, err := filepath.Abs(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Log("original:", abs)

			if out != nil {
				f, err := ioutil.TempFile("", "test-*"+ext)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if _, err := f.Write(out); err != nil {
					t.Fatal(err)
				}
				t.Log("re-encoded:", f.Name())
			}
		}

		// Expect that re-encoding the decoded Node yields exactly the same document
		t.Run(testname + "/re-encode", func(t *testing.T) {
			t.Parallel()

			node, err := newParser(path, bytes.NewReader(txt)).Parse()
			if err != nil {
				showContext(nil)
				t.Fatal(err)
			}

			var out bytes.Buffer
			if err := NewEncoder(&out).Encode(node); err != nil {
				showContext(nil)
				t.Fatal(err)
			}

			if !bytes.Equal(txt, out.Bytes()) {
				showContext(out.Bytes())
				t.Fatal("re-encoded configuration does not match original")
			}
		})

		// Expect that encoding the decoded test case re-decodes to the same value
		t.Run(testname + "/encode", func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			if err := NewEncoder(&out).Encode(expected.value); err != nil {
				showContext(nil)
				t.Fatal(err)
			}

			showContext(out.Bytes())

			var actual interface{}
			if err := NewDecoder(bytes.NewReader(out.Bytes())).Decode(&actual); err != nil {
				t.Fatal(err)
			}

			t.Log("actual:", actual)
			t.Log("expected:", expected.value)

			if err := DeepEqual(actual, expected.value); err != nil {
				t.Fatal(err)
			}
		})

		return nil
	})
}

func BenchmarkStandardSuite(b *testing.B) {
	filepath.Walk("testdata/tests/valid", func(path string, info os.FileInfo, err error) error {
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

		if ext != ".toml" {
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
