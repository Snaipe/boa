// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package json5

import (
	"bytes"
	"io/ioutil"
	"math/big"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"snai.pe/boa/internal/testutil"
)

func TestJSONFull(t *testing.T) {

	type Base struct {
		Int         int
		Int8        int8
		Int16       int16
		Int32       int32
		Int64       int64
		Uint        uint
		Uintptr     uintptr
		Uint8       uint8
		Uint16      uint16
		Uint32      uint32
		Uint64      uint64
		String      string
		Float32     float32
		Float64     float64
		Bool        bool
		BigInt      big.Int
		BigFloat    big.Float
		BigRat      big.Rat
		URL         url.URL
		Time        time.Time
		Regexp      regexp.Regexp
		Map         map[string]interface{}
		List        []interface{}
		PtrInt      *int
		PtrInt8     *int8
		PtrInt16    *int16
		PtrInt32    *int32
		PtrInt64    *int64
		PtrUint     *uint
		PtrUintptr  *uintptr
		PtrUint8    *uint8
		PtrUint16   *uint16
		PtrUint32   *uint32
		PtrUint64   *uint64
		PtrString   *string
		PtrFloat32  *float32
		PtrFloat64  *float64
		PtrBool     *bool
		PtrBigInt   *big.Int
		PtrBigFloat *big.Float
		PtrBigRat   *big.Rat
		PtrURL      *url.URL
		PtrTime     *time.Time
		PtrRegexp   *regexp.Regexp
		PtrMap      *map[string]interface{}
		PtrList     *[]interface{}
	}

	type Val struct {
		Base
		Struct Base
		Ptr    *Base
	}

	for _, tc := range []string{"full", "zero"} {
		t.Run(tc, func(t *testing.T) {
			var v Val

			path, _ := filepath.Abs("testdata/" + tc + ".json")
			newpath, _ := filepath.Abs("testdata/" + tc + ".json.new")

			if err := Load(path, &v); err != nil {
				t.Error(err)
			}

			var out bytes.Buffer
			if err := NewEncoder(&out).Option(JSON()).Encode(v); err != nil {
				t.Fatal(err)
			}

			ioutil.WriteFile(newpath, out.Bytes(), 0666)

			expected, err := ioutil.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Log("original:", path)
			t.Log("re-encoded:", newpath)
			if !bytes.Equal(out.Bytes(), expected) {
				testutil.GitDiffNoIndex(t, path, newpath)
				t.Fatalf("Re-encoded json differs from original")
			}
		})
	}
}

func TestUnexportedFieldsJSONEncoder(t *testing.T) {
	doc := `{
  "exported": 1
}
`
	rdr := strings.NewReader(doc)
	v := struct {
		Exported   int
		unexported int
	}{}
	if err := NewDecoder(rdr).Decode(&v); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := NewEncoder(&out).Option(JSON()).Encode(v); err != nil {
		t.Fatal(err)
	}

	t.Log("original:", doc)
	t.Log("re-encoded:", out.String())

	tmpOrig, err := os.CreateTemp("", "b5.*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer tmpOrig.Close() //nolint: errcheck
	tmpNew, err := os.CreateTemp("", "b5.*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer tmpNew.Close() //nolint: errcheck

	tmpOrigPath := path.Join(os.TempDir(), tmpOrig.Name())
	tmpNewPath := path.Join(os.TempDir(), tmpNew.Name())

	if !bytes.Equal(out.Bytes(), []byte(doc)) {
		testutil.GitDiffNoIndex(t, tmpOrigPath, tmpNewPath)
		t.Fatalf("Re-encoded json differs from original")
	}
}

func FuzzJSONEncoder(f *testing.F) {
	err := filepath.Walk("testdata", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			f.Fatal(err)
		}
		if info.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".json" {
			return nil
		}

		txt, err := ioutil.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(txt)
		return nil
	})
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, txt []byte) {
		t.Log(string(txt))
		objects := []interface{}{
			new(struct{}),
			new(interface{}),
			new([]interface{}),
			new(map[interface{}]interface{}),
		}
		for i := range objects {
			if err := NewDecoder(bytes.NewReader(txt)).Decode(&objects[i]); err != nil {
				continue
			}
			var out bytes.Buffer
			if err := NewEncoder(&out).Option(JSON()).Encode(&objects[i]); err != nil {
				t.Error("failed to re-encode fuzz value", err)
			}
			t.Logf("encoded:\n%v", out.String())
			if err := NewDecoder(&out).Decode(&objects[i]); err != nil {
				t.Error("failed to re-decode fuzz value", err)
			}
		}
	})
}
