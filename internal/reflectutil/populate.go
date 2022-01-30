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
	"time"

	"snai.pe/boa/encoding"
	"snai.pe/boa/syntax"
)

type PopulateFunc func(val reflect.Value, node *syntax.Node) (bool, error)

func Populate(val reflect.Value, node *syntax.Node, convention encoding.NamingConvention, fn PopulateFunc) error {
	_, err := populate(val, node, convention, nil, fn)
	return err
}

func populate(val reflect.Value, node *syntax.Node, convention encoding.NamingConvention, path []string, fn PopulateFunc) (reflect.Value, error) {
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

	toValue := func(node *syntax.Node, path []string) (reflect.Value, error) {
		var (
			rval    reflect.Value
			recurse bool
		)
		switch node.Type {
		case syntax.NodeBool:
			out := node.Value.(bool)
			rval = reflect.ValueOf(&out).Elem()
		case syntax.NodeString:
			out := node.Value.(string)
			rval = reflect.ValueOf(&out).Elem()
		case syntax.NodeNumber:
			switch constv := node.Value.(type) {
			case constant.Value:
				switch constv.Kind() {
				case constant.Int:
					rval = ConstantToInt(constv)
				case constant.Float:
					rval = ConstantToFloat(constv)
				default:
					panic(fmt.Sprintf("unsupported constant kind %v for number node", constv.Kind()))
				}
			case float64: // for infinites
				rval = reflect.ValueOf(&constv).Elem()
			default:
				panic(fmt.Sprintf("unsupported node value %T for number node", constv))
			}
		case syntax.NodeDateTime:
			out := node.Value
			rval = reflect.ValueOf(&out).Elem().Elem()
		case syntax.NodeNil:
			var out interface{}
			rval = reflect.ValueOf(&out).Elem()
		case syntax.NodeMap:
			var out map[interface{}]interface{}
			rval = reflect.ValueOf(&out).Elem()
			recurse = true
		case syntax.NodeList:
			var out []interface{}
			rval = reflect.ValueOf(&out).Elem()
			recurse = true
		default:
			return val, fmt.Errorf("unsupported node type %v", node.Type)
		}

		if recurse {
			_, err := populate(rval, node, convention, path, fn)
			if err != nil {
				return rval, err
			}
		}
		return rval, nil
	}

	if ok, err := fn(val, node); err != nil || ok {
		return val, newErr(err)
	}

	switch rval := val.Interface().(type) {
	case stdenc.TextUnmarshaler:
		if node.Type != syntax.NodeString {
			return val, newNodeErr(syntax.NodeString)
		}
		if err := rval.UnmarshalText([]byte(node.Value.(string))); err != nil {
			return val, newErr(err)
		}
		return val, nil
	case []byte:
		if node.Type != syntax.NodeString {
			return val, newNodeErr(syntax.NodeString)
		}
		val.Set(reflect.ValueOf([]byte(node.Value.(string))))
		return val, nil
	case time.Time:
		if node.Type != syntax.NodeDateTime {
			return val, newNodeErr(syntax.NodeDateTime)
		}
		t, ok := node.Value.(time.Time)
		if !ok {
			panic(fmt.Sprintf("unexpected value type %T for datetime node", node.Value))
		}
		val.Set(reflect.ValueOf(t))
		return val, nil
	}

	switch kind := val.Kind(); kind {

	case reflect.Ptr:
		if val.IsNil() {
			val.Set(reflect.New(typ.Elem()))
		}
		if _, err := populate(val.Elem(), node, convention, path, fn); err != nil {
			return val, err
		}

	case reflect.Bool:
		if node.Type != syntax.NodeBool {
			return val, newNodeErr(syntax.NodeBool)
		}
		val.SetBool(node.Value.(bool))

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if node.Type != syntax.NodeNumber {
			return val, newNodeErr(syntax.NodeNumber)
		}
		i, exact := constant.Int64Val(node.Value.(constant.Value))
		if !exact {
			return val, newErr(fmt.Errorf("cannot assign %v to %v: value does not fit", node.Value, typ))
		}
		val.SetInt(i)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if node.Type != syntax.NodeNumber {
			return val, newNodeErr(syntax.NodeNumber)
		}
		i, exact := constant.Uint64Val(node.Value.(constant.Value))
		if !exact {
			return val, newErr(fmt.Errorf("cannot assign %v to %v: value does not fit", node.Value, typ))
		}
		val.SetUint(i)

	case reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		if node.Type != syntax.NodeNumber {
			return val, newNodeErr(syntax.NodeNumber)
		}
		switch constv := node.Value.(type) {
		case constant.Value:
			f, _ := constant.Float64Val(constv)
			if kind == reflect.Complex64 || kind == reflect.Complex128 {
				val.SetComplex(complex(f, 0))
			} else {
				val.SetFloat(f)
			}
		case float64:
			if kind == reflect.Complex64 || kind == reflect.Complex128 {
				val.SetComplex(complex(constv, 0))
			} else {
				val.SetFloat(constv)
			}
		default:
			panic(fmt.Sprintf("unsupported node value %T for number node", constv))
		}

	case reflect.String:
		if node.Type != syntax.NodeString {
			return val, newNodeErr(syntax.NodeString)
		}
		val.SetString(node.Value.(string))

	case reflect.Array, reflect.Slice:
		if node.Type != syntax.NodeList {
			return val, newNodeErr(syntax.NodeList)
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
				return val, newErr(fmt.Errorf("cannot assign %v to index %d: index out of bounds", node.Value, idx))
			}
			if _, err := populate(val.Index(idx), n, convention, append(path, fmt.Sprintf("[%d]", idx)), fn); err != nil {
				return val, err
			}
			idx++
		}

	case reflect.Map:
		if node.Type != syntax.NodeMap {
			return val, newNodeErr(syntax.NodeMap)
		}
		if val.IsNil() {
			val.Set(reflect.MakeMap(typ))
		}
		for key := node.Child; key != nil; key = key.Sibling {
			value := key.Child

			if key.Type == syntax.NodeKeyPath {
				at := key.Value.([]interface{})
				p := path
				for _, e := range at {
					p = append(p, fmt.Sprintf("[%v]", e))
				}
				rval, err := toValue(value, p)
				if err != nil {
					return val, err
				}
				if err := Set(val, rval, convention, at...); err != nil {
					return val, err
				}
			} else {
				rkey, err := populate(reflect.New(typ.Key()).Elem(), key, convention, path, fn)
				if err != nil {
					return val, err
				}

				rval := reflect.New(typ.Elem()).Elem()
				mval := val.MapIndex(rkey)
				if mval.IsValid() {
					rval.Set(mval)
				}
				set := !mval.IsValid()

				rval, err = populate(rval, value, convention, append(path, fmt.Sprintf("[%v]", rkey.Interface())), fn)
				if err != nil {
					return val, err
				}

				if set {
					val.SetMapIndex(rkey, rval)
				}
			}
		}

	case reflect.Struct:

		if node.Type != syntax.NodeMap {
			return val, newNodeErr(syntax.NodeMap)
		}
		fields := make(map[string]int, typ.NumField()*2)
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if _, ok := LookupTag(field.Tag, "-", false); ok {
				continue
			}
			if nametag, ok := LookupTag(field.Tag, "name", false); ok {
				fields[nametag.Value] = i
			} else {
				fields[convention.Format(field.Name)] = i
			}
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
			case syntax.NodeKeyPath:
				at := key.Value.([]interface{})
				for _, e := range at {
					path = append(path, fmt.Sprintf("[%v]", e))
				}
				rval, err := toValue(value, path)
				if err != nil {
					return val, err
				}
				if err := Set(val, rval, convention, at...); err != nil {
					return val, err
				}
				continue
			default:
				return val, fmt.Errorf("unsupported node type %v", node.Type)
			}

			fieldidx, ok := fields[fieldname]
			if !ok {
				fieldidx, ok = fields[convention.Format(fieldname)]
			}
			if !ok {
				continue
			}
			_, err := populate(val.Field(fieldidx), value, convention, append(path, fmt.Sprintf(".%v", typ.Field(fieldidx).Name)), fn)
			if err != nil {
				return val, err
			}
		}

	case reflect.Interface:
		rval, err := toValue(node, path)
		if err != nil {
			return val, err
		}
		val.Set(rval)

	default:
		return val, fmt.Errorf("cannot assign %v to %v: unsupported type", node.Value, typ)
	}

	return val, nil
}

func Set(val, newval reflect.Value, convention encoding.NamingConvention, at ...interface{}) error {
	if !newval.IsValid() || !val.IsValid() {
		panic("cannot call Set with invalid value")
	}
	if len(at) == 0 {
		for newval.Kind() == reflect.Interface {
			newval = newval.Elem()
		}
		v := val
		for ; v.IsValid() && v.Kind() == reflect.Interface; v = v.Elem() {
			continue
		}
		kind := v.Kind()
		// Some special cases:
		switch {
		// Floating-point numbers are allowed to be converted to complex numbers
		case kind == reflect.Complex64 && (newval.Kind() == reflect.Float32 || newval.Kind() == reflect.Float64):
			val.SetComplex(complex(newval.Float(), 0))
		case kind == reflect.Complex128 && (newval.Kind() == reflect.Float32 || newval.Kind() == reflect.Float64):
			val.SetComplex(complex(newval.Float(), 0))
		// Maps get merged
		case kind == reflect.Map && newval.Kind() == reflect.Map:
			for _, k := range newval.MapKeys() {
				v.SetMapIndex(k, newval.MapIndex(k))
			}
		// For everything else, try normal conversion rules
		default:
			val.Set(newval.Convert(val.Type()))
		}
		return nil
	}

	elem := at[0]
	rval := val
	for rval.Kind() == reflect.Interface {
		rval = rval.Elem()
		if !rval.IsValid() {
			if _, ok := elem.(int); ok {
				var out []interface{}
				rval = reflect.ValueOf(&out).Elem()
			} else {
				var out map[interface{}]interface{}
				rval = reflect.ValueOf(&out).Elem()
			}
		}
	}
	typ := rval.Type()

	velem := reflect.ValueOf(elem)
	switch kind := rval.Kind(); kind {
	case reflect.Ptr:
		if rval.IsNil() {
			rval.Set(reflect.New(typ.Elem()))
			val.Set(rval)
		}
		return Set(rval.Elem(), newval, convention, at...)
	case reflect.Map:
		if !velem.Type().AssignableTo(typ.Key()) {
			return fmt.Errorf("cannot index %v with %T %q", typ, elem, elem)
		}
		if rval.IsNil() {
			rval.Set(reflect.MakeMap(typ))
			val.Set(rval)
		}
		newvval := reflect.New(typ.Elem()).Elem()
		vval := rval.MapIndex(velem)
		if vval.IsValid() {
			newvval.Set(vval)
		}
		err := Set(newvval, newval, convention, at[1:]...)
		if err != nil {
			return err
		}
		rval.SetMapIndex(velem, newvval)
		return nil
	case reflect.Slice, reflect.Array:
		idx, ok := elem.(int)
		if !ok {
			return fmt.Errorf("cannot index slice or array with %T %q", elem, elem)
		}
		if rval.Len() <= idx {
			if kind == reflect.Array {
				return fmt.Errorf("cannot index array at %d: index out of bounds", idx)
			}
			ncap := rval.Cap()
			for idx >= ncap {
				ncap = ncap*2 + 1
			}
			nval := reflect.MakeSlice(typ, idx+1, ncap)
			for i := 0; i < rval.Len(); i++ {
				nval.Index(i).Set(rval.Index(i))
			}
			if err := Set(nval.Index(idx), newval, convention, at[1:]...); err != nil {
				return err
			}
			val.Set(nval)
			return nil
		}
		if err := Set(rval.Index(idx), newval, convention, at[1:]...); err != nil {
			return err
		}
		return nil
	case reflect.Struct:
		fname, ok := elem.(string)
		if !ok {
			return fmt.Errorf("cannot find struct field with non-string name type %T", elem)
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if fname == convention.Format(field.Name) {
				return Set(rval.Field(i), newval, convention, at[1:]...)
			}
		}
		return fmt.Errorf("cannot find struct field with name %v", fname)
	default:
		return fmt.Errorf("cannot index %v with %T key %q", typ, elem, elem)
	}
}

// ConstantToInt returns an int-like value from a constant. It will return an
// int, uint, or *big.Int depending on whether the value fits in the types
// in that order.
func ConstantToInt(constv constant.Value) reflect.Value {
	i, exact := constant.Int64Val(constv)
	if exact {
		return reflect.ValueOf(&i).Elem()
	}
	if constant.Sign(constv) > 0 {
		u, exact := constant.Uint64Val(constv)
		if exact {
			return reflect.ValueOf(&u).Elem()
		}
	}
	out := constant.Val(constv)
	return reflect.ValueOf(&out).Elem().Elem()
}

// ConstantToFloat returns a float-like value from a constant. It will return a
// float64, *big.Rat, or *big.Float depending on whether the value fits in the
// types in that order without losing precision.
func ConstantToFloat(constv constant.Value) reflect.Value {
	f, exact := constant.Float64Val(constv)
	if exact {
		return reflect.ValueOf(&f).Elem()
	}
	out := constant.Val(constv)
	return reflect.ValueOf(&out).Elem().Elem()
}
