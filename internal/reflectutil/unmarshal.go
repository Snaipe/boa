// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package reflectutil

import (
	stdenc "encoding"
	"fmt"
	"go/constant"
	"math/big"
	"net/url"
	"reflect"
	"strings"

	"snai.pe/boa/encoding"
	"snai.pe/boa/syntax"
)

type Unmarshaler interface {
	UnmarshalValue(val reflect.Value, node *syntax.Node) (bool, error)
}

type UnmarshalFunc func(val reflect.Value, node *syntax.Node) (bool, error)

func Unmarshal(val reflect.Value, node *syntax.Node, convention encoding.NamingConvention, unmarshaler Unmarshaler) error {
	_, err := unmarshal(val, node, convention, nil, unmarshaler)
	return err
}

func unmarshal(val reflect.Value, node *syntax.Node, convention encoding.NamingConvention, path []string, unmarshaler Unmarshaler) (reflect.Value, error) {
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
			_, err := unmarshal(rval, node, convention, path, unmarshaler)
			if err != nil {
				return rval, err
			}
		}
		return rval, nil
	}

	if unmarshaler != nil {
		if ok, err := unmarshaler.UnmarshalValue(val, node); err != nil || ok {
			return val, newErr(err)
		}
	}

	switch rval := val.Addr().Interface().(type) {
	case *big.Float:
		if node.Type != syntax.NodeNumber {
			return val, newNodeErr(syntax.NodeNumber)
		}
		switch v := constant.Val(node.Value.(constant.Value)).(type) {
		case int64:
			rval.SetInt64(v)
		case *big.Int:
			rval.SetInt(v)
		case *big.Float:
			rval.Set(v)
		case *big.Rat:
			rval.SetRat(v)
		}
		return val, nil
	case *big.Rat:
		if node.Type != syntax.NodeNumber {
			return val, newNodeErr(syntax.NodeNumber)
		}
		switch v := constant.Val(node.Value.(constant.Value)).(type) {
		case int64:
			rval.SetInt64(v)
		case *big.Int:
			rval.SetInt(v)
		case *big.Float:
			v.Rat(rval)
		case *big.Rat:
			rval.Set(v)
		}
		return val, nil
	case *big.Int:
		if node.Type != syntax.NodeNumber {
			return val, newNodeErr(syntax.NodeNumber)
		}
		switch v := constant.Val(node.Value.(constant.Value)).(type) {
		case int64:
			rval.SetInt64(v)
		case *big.Int:
			rval.Set(v)
		case *big.Float:
			v.Int(rval)
		case *big.Rat:
			rval.Quo(v.Num(), v.Denom())
		}
		return val, nil
	case stdenc.TextUnmarshaler:
		if node.Type != syntax.NodeString {
			return val, newNodeErr(syntax.NodeString)
		}
		if err := rval.UnmarshalText([]byte(node.Value.(string))); err != nil {
			return val, newErr(err)
		}
		return val, nil
	case *[]byte:
		if node.Type != syntax.NodeString {
			return val, newNodeErr(syntax.NodeString)
		}
		val.Set(reflect.ValueOf([]byte(node.Value.(string))))
		return val, nil
	case *url.URL:
		if node.Type != syntax.NodeString {
			return val, newNodeErr(syntax.NodeString)
		}
		if err := rval.UnmarshalBinary([]byte(node.Value.(string))); err != nil {
			return val, newErr(err)
		}
		return val, nil
	}

	switch kind := val.Kind(); kind {

	case reflect.Ptr:
		if node.Type == syntax.NodeNil {
			// We have an explicit nil, therefore we must set the pointer to nil.
			val.Set(reflect.Zero(typ))
		} else {
			if val.IsNil() {
				val.Set(reflect.New(typ.Elem()))
			}
			if _, err := unmarshal(val.Elem(), node, convention, path, unmarshaler); err != nil {
				return val, err
			}
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
			if _, err := unmarshal(val.Index(idx), n, convention, append(path, fmt.Sprintf("[%d]", idx)), unmarshaler); err != nil {
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
				if err := Set(val, rval, convention, unmarshaler, at...); err != nil {
					return val, err
				}
			} else {
				rkey, err := unmarshal(reflect.New(typ.Key()).Elem(), key, convention, path, unmarshaler)
				if err != nil {
					return val, err
				}

				rval := reflect.New(typ.Elem()).Elem()
				mval := val.MapIndex(rkey)
				if mval.IsValid() {
					rval.Set(mval)
				}
				set := !mval.IsValid()

				rval, err = unmarshal(rval, value, convention, append(path, fmt.Sprintf("[%v]", rkey.Interface())), unmarshaler)
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

		fields := VisibleFieldsMap(val, convention, unmarshaler)

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
				if err := Set(val, rval, convention, unmarshaler, at...); err != nil {
					return val, err
				}
				continue
			default:
				return val, fmt.Errorf("unsupported node type %v", node.Type)
			}

			field, ok := fields[fieldname]
			if !ok {
				continue
			}
			_, err := unmarshal(field.Value, value, field.Convention, append(path, fmt.Sprintf(".%v", field.Name)), unmarshaler)
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

func Set(val, newval reflect.Value, convention encoding.NamingConvention, unmarshaler Unmarshaler, at ...interface{}) error {
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
		default:
			// Handle other standard types
			if v.IsValid() {
				switch v.Type() {
				case reflect.TypeOf((*url.URL)(nil)):
					var u url.URL
					if err := u.UnmarshalBinary([]byte(newval.Interface().(string))); err != nil {
						return err
					}
					val.Set(reflect.ValueOf(&u))
					return nil
				case reflect.TypeOf(url.URL{}):
					var u url.URL
					if err := u.UnmarshalBinary([]byte(newval.Interface().(string))); err != nil {
						return err
					}
					val.Set(reflect.ValueOf(u))
					return nil
				}
			}
			// For everything else, try normal conversion rules
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
		return Set(rval.Elem(), newval, convention, unmarshaler, at...)
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
		err := Set(newvval, newval, convention, unmarshaler, at[1:]...)
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
			if err := Set(nval.Index(idx), newval, convention, unmarshaler, at[1:]...); err != nil {
				return err
			}
			val.Set(nval)
			return nil
		}
		if err := Set(rval.Index(idx), newval, convention, unmarshaler, at[1:]...); err != nil {
			return err
		}
		return nil
	case reflect.Struct:
		fname, ok := elem.(string)
		if !ok {
			return fmt.Errorf("cannot find struct field with non-string name type %T", elem)
		}

		fields := VisibleFieldsMap(rval, convention, unmarshaler)

		if field, ok := fields[fname]; ok {
			return Set(field.Value, newval, field.Convention, unmarshaler, at[1:]...)
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

type StructField struct {
	reflect.StructField
	Value      reflect.Value
	Convention encoding.NamingConvention
}

func VisibleFieldsMap(val reflect.Value, convention encoding.NamingConvention, unmarshaler interface{}) map[string]StructField {
	fields := make(map[string]StructField, val.Type().NumField()*2)
	visibleFieldsMap(fields, val, val.Type(), convention, unmarshaler)
	return fields
}

func visibleFieldsMap(fields map[string]StructField, val reflect.Value, typ reflect.Type, convention encoding.NamingConvention, unmarshaler interface{}) {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)

		opts := ParseFieldOpts(field.Tag, unmarshaler, convention)
		if opts.Ignore {
			continue
		}

		elem := val.FieldByIndex(field.Index)
		if field.Anonymous && opts.Name == "" || opts.Inline {
			// Process the field later. This allows local fields
			// to take precendence during unmarshaling.
			continue
		}
		if opts.Name == "" {
			opts.Name = opts.Naming.Format(field.Name)
		}
		if _, ok := fields[opts.Name]; !ok {
			fields[opts.Name] = StructField{field, elem, opts.Naming}
		}
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)

		opts := ParseFieldOpts(field.Tag, unmarshaler, convention)
		if opts.Ignore {
			continue
		}

		elem := val.FieldByIndex(field.Index)
		if field.Anonymous && opts.Name == "" || opts.Inline {
			visibleFieldsMap(fields, elem, field.Type, opts.Naming, unmarshaler)
		}
	}
}
