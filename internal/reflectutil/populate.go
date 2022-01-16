// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package reflectutil

import (
	"encoding"
	"fmt"
	"go/constant"
	"reflect"
	"strings"

	"snai.pe/boa"
	"snai.pe/boa/syntax"
)

type PopulateFunc func(val reflect.Value, node *syntax.Node) (bool, error)

func Populate(val reflect.Value, node *syntax.Node, convention boa.NamingConvention, fn PopulateFunc) error {
	_, err := populate(val, node, convention, fn)
	return err
}

func populate(val reflect.Value, node *syntax.Node, convention boa.NamingConvention, fn PopulateFunc) (reflect.Value, error) {
	typ := val.Type()

	if ok, err := fn(val, node); err != nil || ok {
		return val, err
	}

	if unmarshaler, ok := val.Interface().(encoding.TextUnmarshaler); ok {
		if node.Type != syntax.NodeString {
			return val, syntax.UnexpectedNodeError{syntax.NodeString}
		}
		if err := unmarshaler.UnmarshalText([]byte(node.Value.(string))); err != nil {
			return val, err
		}
		return val, nil
	}

	switch kind := val.Kind(); kind {

	case reflect.Ptr:
		if val.IsNil() {
			val.Set(reflect.New(val.Type()))
		}
		if _, err := populate(val.Elem(), node, convention, fn); err != nil {
			return val, err
		}

	case reflect.Bool:
		if node.Type != syntax.NodeBool {
			return val, syntax.UnexpectedNodeError{syntax.NodeBool}
		}
		val.SetBool(node.Value.(bool))

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if node.Type != syntax.NodeNumber {
			return val, syntax.UnexpectedNodeError{syntax.NodeNumber}
		}
		i, exact := constant.Int64Val(node.Value.(constant.Value))
		if !exact {
			return val, fmt.Errorf("cannot assign %v to %v: value does not fit", node.Value, typ)
		}
		val.SetInt(i)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if node.Type != syntax.NodeNumber {
			return val, syntax.UnexpectedNodeError{syntax.NodeNumber}
		}
		i, exact := constant.Uint64Val(node.Value.(constant.Value))
		if !exact {
			return val, fmt.Errorf("cannot assign %v to %v: value does not fit", node.Value, typ)
		}
		val.SetUint(i)

	case reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		if node.Type != syntax.NodeNumber {
			return val, syntax.UnexpectedNodeError{syntax.NodeNumber}
		}
		f, _ := constant.Float64Val(node.Value.(constant.Value))
		if kind == reflect.Complex64 || kind == reflect.Complex128 {
			val.SetComplex(complex(f, 0))
		} else {
			val.SetFloat(f)
		}

	case reflect.String:
		if node.Type != syntax.NodeString {
			return val, syntax.UnexpectedNodeError{syntax.NodeString}
		}
		val.SetString(node.Value.(string))

	case reflect.Array, reflect.Slice:
		if node.Type != syntax.NodeList {
			return val, syntax.UnexpectedNodeError{syntax.NodeList}
		}
		if kind == reflect.Slice {
			var l int
			for n := node.Child; n != nil; n = n.Sibling {
				l++
			}
			if val.Len() != l {
				val.Set(reflect.MakeSlice(typ, l, l))
			}
		}
		var idx int
		for n := node.Child; n != nil; n = n.Sibling {
			if idx >= val.Len() && kind == reflect.Array {
				return val, fmt.Errorf("cannot assign %v to index %d: index out of bounds", node.Value, idx)
			}
			if _, err := populate(val.Index(idx), n, convention, fn); err != nil {
				return val, err
			}
			idx++
		}

	case reflect.Map:
		if node.Type != syntax.NodeMap {
			return val, syntax.UnexpectedNodeError{syntax.NodeMap}
		}
		if val.IsNil() {
			val.Set(reflect.MakeMap(val.Type()))
		}
		for key := node.Child; key != nil; key = key.Sibling {
			value := key.Child

			rkey, err := populate(reflect.New(typ.Key()).Elem(), key, convention, fn)
			if err != nil {
				return val, err
			}

			rval, err := populate(reflect.New(typ.Elem()).Elem(), value, convention, fn)
			if err != nil {
				return val, err
			}

			val.SetMapIndex(rkey, rval)
		}

	case reflect.Struct:

		if node.Type != syntax.NodeMap {
			return val, syntax.UnexpectedNodeError{syntax.NodeMap}
		}
		fields := make(map[string]int, typ.NumField()*2)
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			fields[strings.ToLower(field.Name)] = i
			fields[convention.Format(field.Name)] = i
		}

		for key := node.Child; key != nil; key = key.Sibling {
			value := key.Child

			var fieldname string
			switch key.Type {
			case syntax.NodeString:
				fieldname = key.Value.(string)
			case syntax.NodeBool:
				if key.Value.(bool) {
					fieldname = "true"
				} else {
					fieldname = "false"
				}
			case syntax.NodeNil:
				fieldname = "null"
			default:
				continue
			}

			fieldidx, ok := fields[fieldname]
			if !ok {
				continue
			}
			_, err := populate(val.Field(fieldidx), value, convention, fn)
			if err != nil {
				return val, err
			}
		}

	case reflect.Interface:
		var (
			out     interface{}
			recurse bool
		)
		switch node.Type {
		case syntax.NodeBool:
			out = node.Value.(bool)
		case syntax.NodeString:
			out = node.Value.(string)
		case syntax.NodeNumber:
			constv := node.Value.(constant.Value)
			switch constv.Kind() {
			case constant.Int:
				i, exact := constant.Int64Val(constv)
				if exact {
					out = i
					break
				}
				if constant.Sign(constv) < 0 {
					return val, fmt.Errorf("cannot assign %v to int64: value does not fit", constv.ExactString())
				}
				out, exact = constant.Uint64Val(constv)
				if !exact {
					return val, fmt.Errorf("cannot assign %v to uint64: value does not fit", constv.ExactString())
				}
			case constant.Float:
				out, _ = constant.Float64Val(constv)
			}
		case syntax.NodeNil:
			break
		case syntax.NodeMap:
			out = map[interface{}]interface{}{}
			recurse = true
		case syntax.NodeList:
			var slice []interface{}
			out = &slice
			recurse = true
		}

		rval := reflect.ValueOf(&out).Elem()
		if recurse {
			_, err := populate(rval.Elem(), node, convention, fn)
			if err != nil {
				return val, err
			}
		}

		switch node.Type {
		case syntax.NodeList:
			val.Set(rval.Elem())
		default:
			val.Set(rval)
		}

	default:
		return val, fmt.Errorf("cannot assign %v to %v: unsupported type", node.Value, typ)
	}

	return val, nil
}
