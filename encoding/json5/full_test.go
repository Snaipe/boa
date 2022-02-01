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
	"path/filepath"
	"testing"
	"time"
)

func TestJSON5Full(t *testing.T) {

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
				t.Fatalf("Re-encoded json5 differs from original")
			}
		})
	}
}
