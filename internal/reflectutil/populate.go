// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package reflectutil

import (
	stdenc "encoding"
	"fmt"
	"go/constant"
	"reflect"
	"strings"

	"snai.pe/boa/encoding"
	"snai.pe/boa/syntax"
)

type PopulateFunc func(val reflect.Value, node *syntax.Node) (bool, error)

func Populate(val reflect.Value, node *syntax.Node, convention encoding.NamingConvention, fn PopulateFunc) error {
	_, err := populate(val, nil, node, convention, nil, fn)
	return err
}

func populate(val reflect.Value, at []interface{}, node *syntax.Node, convention encoding.NamingConvention, path []string, fn PopulateFunc) (reflect.Value, error) {
	typ := val.Type()

	target := func() string {
		if len(path) == 0 {
			return val.Type().String()
		}
		return strings.Join(path, "")
	}

	newErr := func(err error) error {
		return &encoding.LoadError{Cursor: node.Position, Target: target(), Err: err}
	}

	newNodeErr := func(exp syntax.NodeType) error {
		return newErr(fmt.Errorf("config has %v, but expected %v instead", node.Type, exp))
	}

	zeroValFromNode := func() interface{} {
		switch node.Type {
		case syntax.NodeBool:
			return false
		case syntax.NodeString:
			return node.Value.(string)
		case syntax.NodeNumber:
			constv := node.Value.(constant.Value)
			switch constv.Kind() {
			case constant.Int:
				_, exact := constant.Int64Val(constv)
				if exact {
					return int64(0)
				}
				if constant.Sign(constv) > 0 {
					if _, exact = constant.Uint64Val(constv); exact {
						return uint64(0)
					}
				}
			case constant.Float:
				_, exact := constant.Float64Val(constv)
				if exact {
					return float64(0)
				}
			}
			return constant.Val(constv)
		case syntax.NodeMap:
			return make(map[interface{}]interface{})
		case syntax.NodeList:
			return ([]interface{})(nil)
		}
		return nil
	}

	orig := val
	for len(at) > 0 {
		typ = val.Type()
		elem := at[0]
		velem := reflect.ValueOf(elem)
		switch kind := val.Kind(); kind {
		case reflect.Ptr:
			if val.IsNil() {
				val.Set(reflect.New(typ.Elem()))
			}
			val = val.Elem()
			continue
		case reflect.Interface:
			if val.IsNil() {
				if len(at) == 1 {
					val.Set(reflect.ValueOf(zeroValFromNode()))
				} else {
					val.Set(reflect.ValueOf(make(map[interface{}]interface{})))
				}
			}
			val = val.Elem()
			continue
		case reflect.Map:
			if !velem.Type().AssignableTo(typ.Key()) {
				return orig, newErr(fmt.Errorf("cannot index %v with %T %q", typ, elem, elem))
			}
			if val.IsNil() {
				val.Set(reflect.MakeMap(typ))
			}
			mval := val.MapIndex(velem)
			if !mval.IsValid() {
				mval = reflect.New(typ.Elem()).Elem()
				defer func(val, mval, velem reflect.Value) {
					val.SetMapIndex(velem, mval)
				}(val, mval, velem)
			}
			val = mval
		case reflect.Slice, reflect.Array:
			idx, ok := elem.(int)
			if !ok {
				return orig, newErr(fmt.Errorf("cannot index slice or array with %T %q", elem, elem))
			}
			if val.Len() <= idx {
				if idx < val.Cap() {
					val.SetLen(idx+1)
				} else {
					if kind == reflect.Array {
						return orig, newErr(fmt.Errorf("cannot index array at %d: index out of bounds", idx))
					}
					ncap := val.Cap()
					for idx >= ncap {
						ncap = ncap * 2 + 1
					}
					nval := reflect.MakeSlice(typ, idx+1, ncap)
					for i := 0; i < idx; i++ {
						nval.Index(i).Set(val.Index(i))
					}
					val.Set(nval)
				}
			}
			val = val.Index(idx)
		case reflect.Struct:
			fname, ok := elem.(string)
			if !ok {
				return orig, newErr(fmt.Errorf("cannot find struct field with non-string name type %T", elem))
			}
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if fname == convention.Format(field.Name) {
					val = val.Field(i)
					break
				}
			}
		default:
			return orig, newErr(fmt.Errorf("cannot index %v with %T key %q", typ, elem, elem))
		}
		at = at[1:]
	}

	if ok, err := fn(val, node); err != nil || ok {
		return orig, newErr(err)
	}

	switch rval := val.Interface().(type) {
	case stdenc.TextUnmarshaler:
		if node.Type != syntax.NodeString {
			return orig, newNodeErr(syntax.NodeString)
		}
		if err := rval.UnmarshalText([]byte(node.Value.(string))); err != nil {
			return orig, newErr(err)
		}
		return orig, nil
	case []byte:
		if node.Type != syntax.NodeString {
			return orig, newNodeErr(syntax.NodeString)
		}
		val.Set(reflect.ValueOf([]byte(node.Value.(string))))
		return orig, nil
	}

	switch kind := val.Kind(); kind {

	case reflect.Ptr:
		if val.IsNil() {
			val.Set(reflect.New(typ.Elem()))
		}
		if _, err := populate(val.Elem(), nil, node, convention, path, fn); err != nil {
			return orig, err
		}

	case reflect.Bool:
		if node.Type != syntax.NodeBool {
			return orig, newNodeErr(syntax.NodeBool)
		}
		val.SetBool(node.Value.(bool))

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if node.Type != syntax.NodeNumber {
			return orig, newNodeErr(syntax.NodeNumber)
		}
		i, exact := constant.Int64Val(node.Value.(constant.Value))
		if !exact {
			return orig, newErr(fmt.Errorf("cannot assign %v to %v: value does not fit", node.Value, typ))
		}
		val.SetInt(i)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if node.Type != syntax.NodeNumber {
			return orig, newNodeErr(syntax.NodeNumber)
		}
		i, exact := constant.Uint64Val(node.Value.(constant.Value))
		if !exact {
			return orig, newErr(fmt.Errorf("cannot assign %v to %v: value does not fit", node.Value, typ))
		}
		val.SetUint(i)

	case reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		if node.Type != syntax.NodeNumber {
			return orig, newNodeErr(syntax.NodeNumber)
		}
		f, _ := constant.Float64Val(node.Value.(constant.Value))
		if kind == reflect.Complex64 || kind == reflect.Complex128 {
			val.SetComplex(complex(f, 0))
		} else {
			val.SetFloat(f)
		}

	case reflect.String:
		if node.Type != syntax.NodeString {
			return orig, newNodeErr(syntax.NodeString)
		}
		val.SetString(node.Value.(string))

	case reflect.Array, reflect.Slice:
		if node.Type != syntax.NodeList {
			return orig, newNodeErr(syntax.NodeList)
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
				return orig, newErr(fmt.Errorf("cannot assign %v to index %d: index out of bounds", node.Value, idx))
			}
			if _, err := populate(val.Index(idx), nil, n, convention, append(path, fmt.Sprintf("[%d]", idx)), fn); err != nil {
				return orig, err
			}
			idx++
		}

	case reflect.Map:
		if node.Type != syntax.NodeMap {
			return orig, newNodeErr(syntax.NodeMap)
		}
		if val.IsNil() {
			val.Set(reflect.MakeMap(typ))
		}
		for key := node.Child; key != nil; key = key.Sibling {
			var (
				at   []interface{}
				rkey reflect.Value
			)
			if key.Type == syntax.NodeKeyPath {
				at = key.Value.([]interface{})
				rkey, at = reflect.ValueOf(at[0]), at[1:]
			} else {
				var err error
				rkey, err = populate(reflect.New(typ.Key()).Elem(), nil, key, convention, path, fn)
				if err != nil {
					return orig, err
				}
			}
			value := key.Child

			rval, err := populate(reflect.New(typ.Elem()).Elem(), at, value, convention, append(path, fmt.Sprintf("[%v]", rkey.Interface())), fn)
			if err != nil {
				return orig, err
			}

			val.SetMapIndex(rkey, rval)
		}

	case reflect.Struct:

		if node.Type != syntax.NodeMap {
			return orig, newNodeErr(syntax.NodeMap)
		}
		fields := make(map[string]int, typ.NumField()*2)
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			fields[convention.Format(field.Name)] = i
		}

		for key := node.Child; key != nil; key = key.Sibling {
			value := key.Child

			var (
				at        []interface{}
				fieldname string
			)
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
			case syntax.NodeKeyPath:
				at = key.Value.([]interface{})
				var ok bool
				fieldname, ok = at[0].(string)
				if !ok {
					return orig, newErr(fmt.Errorf("cannot find struct field with non-string name type %T", at[0]))
				}
				at = at[1:]
			default:
				continue
			}

			fieldidx, ok := fields[fieldname]
			if !ok {
				fieldidx, ok = fields[convention.Format(fieldname)]
			}
			if !ok {
				continue
			}
			_, err := populate(val.Field(fieldidx), at, value, convention, append(path, fmt.Sprintf(".%v", typ.Field(fieldidx).Name)), fn)
			if err != nil {
				return orig, err
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
				if constant.Sign(constv) > 0 {
					out, exact = constant.Uint64Val(constv)
					if exact {
						break
					}
				}
				out = constant.Val(constv)
			case constant.Float:
				f, exact := constant.Float64Val(constv)
				if exact {
					out = f
					break
				}
				out = constant.Val(constv)
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
			_, err := populate(rval.Elem(), nil, node, convention, path, fn)
			if err != nil {
				return orig, err
			}
		}

		switch node.Type {
		case syntax.NodeList:
			val.Set(rval.Elem())
		default:
			val.Set(rval)
		}

	default:
		return orig, fmt.Errorf("cannot assign %v to %v: unsupported type", node.Value, typ)
	}

	return orig, nil
}
