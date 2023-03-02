// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package toml

import (
	"bytes"
	"io/ioutil"
	"math/big"
	"net/url"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"snai.pe/boa/internal/testutil"
)

func TestTOMLFull(t *testing.T) {

	type Base struct {
		Int           int
		Int8          int8
		Int16         int16
		Int32         int32
		Int64         int64
		Uint          uint
		Uintptr       uintptr
		Uint8         uint8
		Uint16        uint16
		Uint32        uint32
		Uint64        uint64
		String        string
		Map           map[string]interface{} // interleave compound types to test stable sorting
		Float32       float32
		Float64       float64
		Bool          bool
		ListOfTables  []map[string]interface{} // interleave compound types to test stable sorting
		BigInt        big.Int
		BigFloat      big.Float
		BigRat        big.Rat
		URL           url.URL
		Time          time.Time
		Regexp        regexp.Regexp
		LocalDateTime LocalDateTime
		LocalDate     LocalDate
		LocalTime     LocalTime
		List          []interface{}
	}

	type BaseWithPtr struct {
		Base
		Interface        interface{}
		PtrInt           *int
		PtrInt8          *int8
		PtrInt16         *int16
		PtrInt32         *int32
		PtrInt64         *int64
		PtrUint          *uint
		PtrUintptr       *uintptr
		PtrUint8         *uint8
		PtrUint16        *uint16
		PtrUint32        *uint32
		PtrUint64        *uint64
		PtrString        *string
		PtrFloat32       *float32
		PtrFloat64       *float64
		PtrBool          *bool
		PtrBigInt        *big.Int
		PtrBigFloat      *big.Float
		PtrBigRat        *big.Rat
		PtrURL           *url.URL
		PtrTime          *time.Time
		PtrRegexp        *regexp.Regexp
		PtrLocalDateTime *LocalDateTime
		PtrLocalDate     *LocalDate
		PtrLocalTime     *LocalTime
		PtrMap           *map[string]interface{}
		PtrList          *[]interface{}
		PtrListOfTables  *[]map[string]interface{}
		PtrInterface     *interface{}
	}

	type Val struct {
		Base
		Struct Base
	}

	type ValWithPtr struct {
		BaseWithPtr
		Struct BaseWithPtr
		Ptr    *BaseWithPtr
	}

	tcases := []struct {
		Name string
		Val  interface{}
	}{
		{"full", &ValWithPtr{}},
		{"zero", &Val{}},
	}

	for _, tc := range tcases {
		t.Run(tc.Name, func(t *testing.T) {
			path, _ := filepath.Abs("testdata/" + tc.Name + ".toml")
			newpath, _ := filepath.Abs("testdata/" + tc.Name + ".toml.new")

			if err := Load(path, tc.Val); err != nil {
				t.Error(err)
			}

			var out bytes.Buffer
			if err := NewEncoder(&out).Encode(tc.Val); err != nil {
				t.Fatal(err)
			}

			ioutil.WriteFile(newpath, out.Bytes(), 0666)
			t.Log("original:", path)
			t.Log("re-encoded:", newpath)

			expected, err := ioutil.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out.Bytes(), expected) {
				testutil.GitDiffNoIndex(t, path, newpath)
				t.Fatalf("Re-encoded toml differs from original")
			}
		})
	}
}
