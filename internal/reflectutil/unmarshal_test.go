// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package reflectutil

import (
	"go/constant"
	"net/url"
	"reflect"
	"regexp"
	"testing"

	"snai.pe/boa/encoding"
	"snai.pe/boa/syntax"
)

var (
	testNode        syntax.Value
	testNodeKeypath syntax.Value
)

// testKeyPath is a test-only implementation of syntax.KeyPather.
type testKeyPath struct {
	syntax.Node
	path []interface{}
}

func (kp *testKeyPath) KeyPathComponents() []interface{} { return kp.path }

type T struct {
	Str        string
	Bytes      []byte
	Int        int
	Int8       int8
	Int16      int16
	Int32      int32
	Int64      int64
	Uint       uint
	Uint8      uint8
	Uint16     uint16
	Uint32     uint32
	Uint64     uint64
	Uintptr    uintptr
	Float32    float32
	Float64    float64
	Complex64  complex64
	Complex128 complex128
	Map        map[interface{}]interface{}
	List       []interface{}
	Struct     *T
	URL        *url.URL
	Regexp     *regexp.Regexp
}

var expectedNest = T{
	Str:        "string",
	Bytes:      []byte("string"),
	Int:        int(1),
	Int8:       int8(1),
	Int16:      int16(1),
	Int32:      int32(1),
	Int64:      int64(1),
	Uint:       uint(1),
	Uint8:      uint8(1),
	Uint16:     uint16(1),
	Uint32:     uint32(1),
	Uint64:     uint64(1),
	Uintptr:    uintptr(1),
	Float32:    float32(1),
	Float64:    float64(1),
	Complex64:  complex(float32(1), 0),
	Complex128: complex(float64(1), 0),
	URL:        &url.URL{Scheme: "https", Host: "snai.pe", Path: "/boa"},
	Regexp:     regexp.MustCompile("^.*$"),
}

var expected = expectedNest

type nodespec struct {
	key   string
	value syntax.Value
}

func makeNodes() []nodespec {
	return []nodespec{
		{"str", &syntax.String{Value: "string"}},
		{"bytes", &syntax.String{Value: "string"}},
		{"int", &syntax.Number{Value: constant.MakeInt64(1)}},
		{"int8", &syntax.Number{Value: constant.MakeInt64(1)}},
		{"int16", &syntax.Number{Value: constant.MakeInt64(1)}},
		{"int32", &syntax.Number{Value: constant.MakeInt64(1)}},
		{"int64", &syntax.Number{Value: constant.MakeInt64(1)}},
		{"uint", &syntax.Number{Value: constant.MakeUint64(1)}},
		{"uint8", &syntax.Number{Value: constant.MakeUint64(1)}},
		{"uint16", &syntax.Number{Value: constant.MakeUint64(1)}},
		{"uint32", &syntax.Number{Value: constant.MakeUint64(1)}},
		{"uint64", &syntax.Number{Value: constant.MakeUint64(1)}},
		{"uintptr", &syntax.Number{Value: constant.MakeUint64(1)}},
		{"float32", &syntax.Number{Value: constant.MakeFloat64(1)}},
		{"float64", &syntax.Number{Value: constant.MakeFloat64(1)}},
		{"complex64", &syntax.Number{Value: constant.MakeFloat64(1)}},
		{"complex128", &syntax.Number{Value: constant.MakeFloat64(1)}},
		{"url", &syntax.String{Value: "https://snai.pe/boa"}},
		{"regexp", &syntax.String{Value: "^.*$"}},
	}
}

func buildMap(nodes []nodespec) *syntax.Map {
	m := &syntax.Map{}
	for _, n := range nodes {
		m.Entries = append(m.Entries, &syntax.MapEntry{
			Key:   &syntax.String{Value: n.key},
			Value: n.value,
		})
	}
	return m
}

func buildList(nodes []nodespec) *syntax.List {
	l := &syntax.List{}
	for _, n := range nodes {
		l.Items = append(l.Items, n.value)
	}
	return l
}

func init() {
	expected.Struct = &expectedNest
	expected.Map = map[interface{}]interface{}{
		"str":        "string",
		"bytes":      "string",
		"int":        int64(1),
		"int8":       int64(1),
		"int16":      int64(1),
		"int32":      int64(1),
		"int64":      int64(1),
		"uint":       int64(1),
		"uint8":      int64(1),
		"uint16":     int64(1),
		"uint32":     int64(1),
		"uint64":     int64(1),
		"uintptr":    int64(1),
		"float32":    float64(1),
		"float64":    float64(1),
		"complex64":  float64(1),
		"complex128": float64(1),
		"url":        "https://snai.pe/boa",
		"regexp":     "^.*$",
	}
	expected.List = []interface{}{
		"string",
		"string",
		int64(1),
		int64(1),
		int64(1),
		int64(1),
		int64(1),
		int64(1),
		int64(1),
		int64(1),
		int64(1),
		int64(1),
		int64(1),
		float64(1),
		float64(1),
		float64(1),
		float64(1),
		"https://snai.pe/boa",
		"^.*$",
	}

	nodes := makeNodes()
	m2 := buildMap(nodes)

	m1 := buildMap(nodes)
	m1.Entries = append(m1.Entries,
		&syntax.MapEntry{Key: &syntax.String{Value: "map"}, Value: m2},
		&syntax.MapEntry{Key: &syntax.String{Value: "list"}, Value: buildList(nodes)},
		&syntax.MapEntry{Key: &syntax.String{Value: "struct"}, Value: m2},
	)
	testNode = m1

	// Build the keypath test node
	mkp := &syntax.Map{}
	for _, n := range nodes {
		mkp.Entries = append(mkp.Entries, &syntax.MapEntry{
			Key:   &testKeyPath{path: []interface{}{"nested", n.key}},
			Value: n.value,
		})
	}
	for _, n := range nodes {
		mkp.Entries = append(mkp.Entries, &syntax.MapEntry{
			Key:   &testKeyPath{path: []interface{}{"nested", "map", n.key}},
			Value: n.value,
		})
	}
	for _, n := range nodes {
		mkp.Entries = append(mkp.Entries, &syntax.MapEntry{
			Key:   &testKeyPath{path: []interface{}{"nested", "struct", n.key}},
			Value: n.value,
		})
	}
	for i, n := range nodes {
		mkp.Entries = append(mkp.Entries, &syntax.MapEntry{
			Key:   &testKeyPath{path: []interface{}{"nested", "list", i}},
			Value: n.value,
		})
	}
	testNodeKeypath = mkp
}

func TestUnmarshal(t *testing.T) {

	t.Run("nested", func(t *testing.T) {
		var actual T
		err := Unmarshal(reflect.ValueOf(&actual).Elem(), testNode, encoding.CamelCase, false, nil)
		if err != nil {
			t.Fatal(err)
		}

		if err := DeepEqual(actual, expected, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("keypath", func(t *testing.T) {
		type T2 struct {
			Nested T
		}
		var (
			act T2
			exp = T2{Nested: expected}
		)

		err := Unmarshal(reflect.ValueOf(&act).Elem(), testNodeKeypath, encoding.CamelCase, false, nil)
		if err != nil {
			t.Fatal(err)
		}

		if err := DeepEqual(act, exp, nil); err != nil {
			t.Log(act)
			t.Log(exp)
			t.Fatal(err)
		}
	})

}
