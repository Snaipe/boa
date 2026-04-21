// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package yaml

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"math/big"
	"net/url"
	"path/filepath"
	"regexp"
	"testing"
	"time"
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

func TestYAMLFull(t *testing.T) {
	var v FullVal

	path, _ := filepath.Abs("testdata/full.yaml")
	if err := Load(path, &v); err != nil {
		t.Fatal(err)
	}

	if v.Int != -42424242 {
		t.Errorf("Int = %d, want -42424242", v.Int)
	}
	if v.String != "Some string" {
		t.Errorf("String = %q, want %q", v.String, "Some string")
	}
	if v.Struct.Int != -42424242 {
		t.Errorf("Struct.Int = %d, want -42424242", v.Struct.Int)
	}
	if v.Ptr == nil {
		t.Fatal("Ptr is nil")
	}
	if v.Ptr.Int != -42424242 {
		t.Errorf("Ptr.Int = %d, want -42424242", v.Ptr.Int)
	}
}

func BenchmarkParse(b *testing.B) {
	txt, err := ioutil.ReadFile("testdata/full.yaml")
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(txt)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := newParser(context.Background(), bytes.NewReader(txt), DefaultSchema).Parse(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecode(b *testing.B) {
	txt, err := ioutil.ReadFile("testdata/full.yaml")
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
	path, _ := filepath.Abs("testdata/full.yaml")
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
