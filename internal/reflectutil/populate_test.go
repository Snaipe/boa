// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package reflectutil

import (
	"go/constant"
	"reflect"
	"testing"

	"snai.pe/boa/encoding"
	"snai.pe/boa/syntax"
)

var testNode *syntax.Node

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
}

var expected = expectedNest

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
	}

	type nodespec struct {
		key   string
		typ   syntax.NodeType
		value interface{}
	}

	nodes := []nodespec{
		{ "str",        syntax.NodeString, "string" },
		{ "bytes",      syntax.NodeString, "string" },
		{ "int",        syntax.NodeNumber, constant.MakeInt64(1) },
		{ "int8",       syntax.NodeNumber, constant.MakeInt64(1) },
		{ "int16",      syntax.NodeNumber, constant.MakeInt64(1) },
		{ "int32",      syntax.NodeNumber, constant.MakeInt64(1) },
		{ "int64",      syntax.NodeNumber, constant.MakeInt64(1) },
		{ "uint",       syntax.NodeNumber, constant.MakeUint64(1) },
		{ "uint8",      syntax.NodeNumber, constant.MakeUint64(1) },
		{ "uint16",     syntax.NodeNumber, constant.MakeUint64(1) },
		{ "uint32",     syntax.NodeNumber, constant.MakeUint64(1) },
		{ "uint64",     syntax.NodeNumber, constant.MakeUint64(1) },
		{ "uintptr",    syntax.NodeNumber, constant.MakeUint64(1) },
		{ "float32",    syntax.NodeNumber, constant.MakeFloat64(1) },
		{ "float64",    syntax.NodeNumber, constant.MakeFloat64(1) },
		{ "complex64",  syntax.NodeNumber, constant.MakeFloat64(1) },
		{ "complex128", syntax.NodeNumber, constant.MakeFloat64(1) },
	}

	m1 := syntax.Node{Type: syntax.NodeMap}
	m2 := m1
	l := syntax.Node{Type: syntax.NodeList}

	fillNode := func(root *syntax.Node, kv bool) **syntax.Node {
		prev := &root.Child
		for _, n := range nodes {
			node := &syntax.Node{
				Type:  n.typ,
				Value: n.value,
			}
			if kv {
				node = &syntax.Node{
					Type:  syntax.NodeString,
					Value: n.key,
					Child: node,
				}
			}
			*prev = node
			prev = &node.Sibling
		}
		return prev
	}

	fillNode(&l, false)
	fillNode(&m2, true)
	prev := fillNode(&m1, true)

	mapnode := &syntax.Node{
		Type:  syntax.NodeString,
		Value: "map",
		Child: &m2,
	}
	*prev = mapnode
	prev = &mapnode.Sibling

	listnode := &syntax.Node{
		Type:  syntax.NodeString,
		Value: "list",
		Child: &l,
	}
	*prev = listnode
	prev = &listnode.Sibling

	structnode := &syntax.Node{
		Type:  syntax.NodeString,
		Value: "struct",
		Child: &m2,
	}
	*prev = structnode
	prev = &structnode.Sibling

	testNode = &m1
}

func noop(reflect.Value, *syntax.Node) (bool, error) {
	return false, nil
}

func TestPopulate(t *testing.T) {

	var actual T
	err := Populate(reflect.ValueOf(&actual).Elem(), testNode, encoding.CamelCase, noop)
	if err != nil {
		t.Fatal(err)
	}

	if err := DeepEqual(actual, expected); err != nil {
		t.Log(actual.Struct)
		t.Log(expected.Struct)
		t.Fatal(err)
	}

}
