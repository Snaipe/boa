// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package json5

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/ioutil"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"snai.pe/boa/internal/testutil"
)

type FullBase struct {
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

type FullVal struct {
	FullBase
	Struct FullBase
	Ptr    *FullBase
}

func TestJSON5Full(t *testing.T) {

	for _, tc := range []string{"full", "zero"} {
		t.Run(tc, func(t *testing.T) {
			var v FullVal

			path, _ := filepath.Abs("testdata/" + tc + ".json5")
			newpath, _ := filepath.Abs("testdata/" + tc + ".json5.new")

			if err := Load(path, &v); err != nil {
				t.Error(err)
			}

			var out bytes.Buffer
			if err := NewEncoder(&out).Encode(v); err != nil {
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
				t.Fatalf("Re-encoded json5 differs from original")
			}
		})
	}
}

func BenchmarkParse(b *testing.B) {
	txt, err := ioutil.ReadFile("testdata/full.json5")
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(txt)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := newParser(context.Background(), bytes.NewReader(txt)).Parse(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecode(b *testing.B) {
	txt, err := ioutil.ReadFile("testdata/full.json5")
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(txt)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v FullVal
		if err := NewDecoder(bytes.NewReader(txt)).Decode(&v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncode(b *testing.B) {
	var v FullVal
	path, _ := filepath.Abs("testdata/full.json5")
	if err := Load(path, &v); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := NewEncoder(io.Discard).Encode(v); err != nil {
			b.Fatal(err)
		}
	}
}

// StdlibBase mirrors FullBase with types compatible with encoding/json.
// BigInt, BigFloat, BigRat are omitted because encoding/json does not
// support arbitrary-precision numbers.
type StdlibBase struct {
	Int        int
	Int8       int8
	Int16      int16
	Int32      int32
	Int64      int64
	Uint       uint
	Uintptr    uintptr
	Uint8      uint8
	Uint16     uint16
	Uint32     uint32
	Uint64     uint64
	String     string
	Float32    float32
	Float64    float64
	Bool       bool
	URL        string
	Time       time.Time
	Regexp     string
	Map        map[string]interface{}
	List       []interface{}
	PtrInt     *int
	PtrInt8    *int8
	PtrInt16   *int16
	PtrInt32   *int32
	PtrInt64   *int64
	PtrUint    *uint
	PtrUintptr *uintptr
	PtrUint8   *uint8
	PtrUint16  *uint16
	PtrUint32  *uint32
	PtrUint64  *uint64
	PtrString  *string
	PtrFloat32 *float32
	PtrFloat64 *float64
	PtrBool    *bool
	PtrURL     *string
	PtrTime    *time.Time
	PtrRegexp  *string
	PtrMap     *map[string]interface{}
	PtrList    *[]interface{}
}

type StdlibVal struct {
	StdlibBase
	Struct StdlibBase
	Ptr    *StdlibBase
}

func BenchmarkDecodeStdlib(b *testing.B) {
	txt, err := ioutil.ReadFile("testdata/full.json")
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(txt)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v StdlibVal
		if err := json.Unmarshal(txt, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeStdlib(b *testing.B) {
	txt, err := ioutil.ReadFile("testdata/full.json")
	if err != nil {
		b.Fatal(err)
	}
	var v StdlibVal
	if err := json.Unmarshal(txt, &v); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(v); err != nil {
			b.Fatal(err)
		}
	}
}

func FuzzEncoder(f *testing.F) {
	err := filepath.Walk("testdata", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			f.Fatal(err)
		}
		if info.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".json" && ext != ".json5" {
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
			if err := NewEncoder(&out).Encode(&objects[i]); err != nil {
				t.Error("failed to re-encode fuzz value", err)
			}
			t.Logf("encoded:\n%v", out.String())
			if err := NewDecoder(&out).Decode(&objects[i]); err != nil {
				t.Error("failed to re-decode fuzz value", err)
			}
		}
	})
}
