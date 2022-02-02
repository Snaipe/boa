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
	_, err := unmarshal(val, node, convention, nil, false, unmarshaler)
	return err
}

func unmarshal(val reflect.Value, node *syntax.Node, convention encoding.NamingConvention, path []string, merge bool, unmarshaler Unmarshaler) (reflect.Value, error) {
	typ := val.Type()

	target := func() string {
		if len(path) == 0 {
			return val.Type().String()
		}
		return strings.Join(path, "")
	}

	newErr := func(err error) error {
		if err == nil {
			return nil
		}
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
		case syntax.NodeBool, syntax.NodeString, syntax.NodeNil:
			out := node.Value.(bool)
			rval = reflect.ValueOf(&out).Elem()
		case syntax.NodeNumber:
			switch out := node.Value.(type) {
			case constant.Value:
				rval = reflect.ValueOf(&out).Elem()
			case float64: // for infinities
				rval = reflect.ValueOf(&out).Elem()
			default:
				panic(fmt.Sprintf("unsupported node value %T for number node", out))
			}
		case syntax.NodeDateTime:
			out := node.Value
			rval = reflect.ValueOf(&out).Elem().Elem()
		case syntax.NodeMap:
			var out map[interface{}]interface{}
			rval = reflect.ValueOf(&out).Elem()
			recurse = true
		case syntax.NodeList:
			var out []interface{}
			rval = reflect.ValueOf(&out).Elem()
			recurse = true
		default:
			return rval, fmt.Errorf("unsupported node type %v", node.Type)
		}

		if recurse {
			_, err := unmarshal(rval, node, convention, path, merge, unmarshaler)
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

	if scalar, err := SetScalar(val, node.Value, node.Type); scalar {
		return val, err
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
			if _, err := unmarshal(val.Elem(), node, convention, path, merge, unmarshaler); err != nil {
				return val, err
			}
		}

	case reflect.Array, reflect.Slice:
		if node.Type == syntax.NodeNil {
			val.Set(reflect.Zero(typ))
			return val, nil
		}
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
			if _, err := unmarshal(val.Index(idx), n, convention, append(path, fmt.Sprintf("[%d]", idx)), merge, unmarshaler); err != nil {
				return val, err
			}
			idx++
		}

	case reflect.Map:
		if node.Type == syntax.NodeNil {
			val.Set(reflect.Zero(typ))
			return val, nil
		}
		if node.Type != syntax.NodeMap {
			return val, newNodeErr(syntax.NodeMap)
		}
		if val.IsNil() || !merge {
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
				if err := Set(val, value, convention, unmarshaler, at...); err != nil {
					return val, err
				}
			} else {
				rkey, err := unmarshal(reflect.New(typ.Key()).Elem(), key, convention, path, merge, unmarshaler)
				if err != nil {
					return val, err
				}

				rval := reflect.New(typ.Elem()).Elem()
				mval := val.MapIndex(rkey)
				if mval.IsValid() {
					rval.Set(mval)
				}
				set := !mval.IsValid()

				rval, err = unmarshal(rval, value, convention, append(path, fmt.Sprintf("[%v]", rkey.Interface())), merge, unmarshaler)
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
		if !merge {
			val.Set(reflect.Zero(typ))
		}

		_, fields := VisibleFields(val, convention, unmarshaler)

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
				if err := Set(val, value, convention, unmarshaler, at...); err != nil {
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
			_, err := unmarshal(field.Value, value, field.Options.Naming, append(path, fmt.Sprintf(".%v", field.Name)), merge, unmarshaler)
			if err != nil {
				return val, err
			}
		}

	case reflect.Interface:
		if merge && !val.IsNil() {
			if _, err := unmarshal(val.Elem(), node, convention, path, merge, unmarshaler); err != nil {
				return val, err
			}
		} else {
			rval, err := toValue(node, path)
			if err != nil {
				return val, err
			}
			val.Set(rval)
		}

	default:
		return val, fmt.Errorf("cannot assign %v to %v: unsupported type", node.Value, typ)
	}

	return val, nil
}

type pathError struct {
	Full []interface{}
	Path []interface{}
	Err error
}

func (e *pathError) Error() string {
	var out strings.Builder
	out.WriteString("<value>")
	for _, p := range e.Path {
		switch e := p.(type) {
		case int:
			fmt.Fprintf(&out, "[%d]", e)
		default:
			fmt.Fprintf(&out, ".%v", e)
		}
	}
	fmt.Fprintf(&out, ": %v", e.Err)
	return out.String()
}

func (e *pathError) Unwrap() error {
	return e.Err
}

func Set(val reflect.Value, node *syntax.Node, convention encoding.NamingConvention, unmarshaler Unmarshaler, at ...interface{}) (outerr error) {
	if !val.IsValid() {
		panic("cannot call Set with invalid value")
	}

	wrapErr := func(err error) error {
		if err == nil {
			return nil
		}
		if pe, ok := err.(*pathError); ok {
			pe.Path = at[:len(at)-len(pe.Full)+1]
			pe.Full = at
		} else {
			err = &pathError{Full: at, Err: err}
		}
		return err
	}

	switch kind := val.Kind(); kind {
	case reflect.Ptr:
		if val.IsNil() {
			val.Set(reflect.New(val.Type().Elem()))
		}
		return Set(val.Elem(), node, convention, unmarshaler, at...)
	}

	if len(at) == 0 {
		newval, err := unmarshal(val, node, convention, nil, true, unmarshaler)
		if err == nil {
			val.Set(newval)
		}
		return err
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
		return Set(rval.Elem(), node, convention, unmarshaler, at...)
	case reflect.Map:
		if !velem.Type().AssignableTo(typ.Key()) {
			return wrapErr(fmt.Errorf("cannot index %v with %T %q", typ, elem, elem))
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
		err := Set(newvval, node, convention, unmarshaler, at[1:]...)
		if err != nil {
			return wrapErr(err)
		}
		rval.SetMapIndex(velem, newvval)
		return nil
	case reflect.Slice, reflect.Array:
		idx, ok := elem.(int)
		if !ok {
			return wrapErr(fmt.Errorf("cannot index slice or array with %T %q", elem, elem))
		}
		if rval.Len() <= idx {
			if kind == reflect.Array {
				return wrapErr(fmt.Errorf("cannot index array at %d: index out of bounds", idx))
			}
			ncap := rval.Cap()
			for idx >= ncap {
				ncap = ncap*2 + 1
			}
			nval := reflect.MakeSlice(typ, idx+1, ncap)
			for i := 0; i < rval.Len(); i++ {
				nval.Index(i).Set(rval.Index(i))
			}
			if err := Set(nval.Index(idx), node, convention, unmarshaler, at[1:]...); err != nil {
				return wrapErr(err)
			}
			val.Set(nval)
			return nil
		}
		if err := Set(rval.Index(idx), node, convention, unmarshaler, at[1:]...); err != nil {
			return wrapErr(err)
		}
		return nil
	case reflect.Struct:
		fname, ok := elem.(string)
		if !ok {
			return nil
		}

		_, fields := VisibleFields(rval, convention, unmarshaler)

		if field, ok := fields[fname]; ok {
			return wrapErr(Set(field.Value, node, field.Options.Naming, unmarshaler, at[1:]...))
		}
		return nil
	default:
		return wrapErr(fmt.Errorf("cannot index %v with %T key %q", typ, elem, elem))
	}
}

func SetScalar(to reflect.Value, value interface{}, nodetype syntax.NodeType) (bool, error) {
	if !to.IsValid() {
		return false, fmt.Errorf("invalid value is not a scalar")
	}

	newNodeErr := func(exp syntax.NodeType) error {
		return fmt.Errorf("config has %v, but expected %v instead", nodetype, exp)
	}

	if to.CanAddr() {
		switch ptr := to.Addr().Interface().(type) {
		case *big.Float:
			if nodetype != syntax.NodeNumber {
				return true, newNodeErr(syntax.NodeNumber)
			}
			switch constv := value.(type) {
			case constant.Value:
				switch v := constant.Val(constv).(type) {
				case int64:
					ptr.SetInt64(v)
				case *big.Int:
					ptr.SetInt(v)
				case *big.Float:
					ptr.Set(v)
				case *big.Rat:
					ptr.SetRat(v)
				default:
					panic(fmt.Sprintf("constant.Val returned an unexpected type %T", v))
				}
			case float64: // infinity and NaN
				ptr.Set(big.NewFloat(constv))
			default:
				panic(fmt.Sprintf("unsupported node value %T for number node", constv))
			}
			return true, nil
		case *big.Rat:
			if nodetype != syntax.NodeNumber {
				return true, newNodeErr(syntax.NodeNumber)
			}
			switch constv := value.(type) {
			case constant.Value:
				switch v := constant.Val(value.(constant.Value)).(type) {
				case int64:
					ptr.SetInt64(v)
				case *big.Int:
					ptr.SetInt(v)
				case *big.Float:
					v.Rat(ptr)
				case *big.Rat:
					ptr.Set(v)
				}
			case float64: // infinity and NaN
				return true, fmt.Errorf("cannot represent %f in *big.Rat", constv)
			default:
				panic(fmt.Sprintf("unsupported node value %T for number node", constv))
			}
			return true, nil
		case *big.Int:
			if nodetype != syntax.NodeNumber {
				return true, newNodeErr(syntax.NodeNumber)
			}
			switch constv := value.(type) {
			case constant.Value:
				switch v := constant.Val(value.(constant.Value)).(type) {
				case int64:
					ptr.SetInt64(v)
				case *big.Int:
					ptr.Set(v)
				case *big.Float:
					v.Int(ptr)
				case *big.Rat:
					ptr.Quo(v.Num(), v.Denom())
				}
			case float64: // infinity and NaN
				return true, fmt.Errorf("cannot represent %f in *big.Int", constv)
			default:
				panic(fmt.Sprintf("unsupported node value %T for number node", constv))
			}
			return true, nil
		case stdenc.TextUnmarshaler:
			if nodetype != syntax.NodeString {
				return true, newNodeErr(syntax.NodeString)
			}
			if err := ptr.UnmarshalText([]byte(value.(string))); err != nil {
				return true, err
			}
			return true, nil
		case *[]byte:
			if nodetype != syntax.NodeString {
				return true, newNodeErr(syntax.NodeString)
			}
			to.Set(reflect.ValueOf([]byte(value.(string))))
			return true, nil
		case *url.URL:
			if nodetype != syntax.NodeString {
				return true, newNodeErr(syntax.NodeString)
			}
			if err := ptr.UnmarshalBinary([]byte(value.(string))); err != nil {
				return true, err
			}
			return true, nil
		}
	}

	// Untyped interface{} gets filled in with an appropriate scalar value.
	if to.Type() == reflect.TypeOf((*interface{})(nil)).Elem() {
		if value != nil {
			to.Set(ToScalar(reflect.ValueOf(value)))
			return true, nil
		} else if nodetype == syntax.NodeNil {
			to.Set(reflect.Zero(to.Type()))
			return true, nil
		}
	}

	switch kind := to.Kind(); kind {

	case reflect.Bool:
		if nodetype != syntax.NodeBool {
			return true, newNodeErr(syntax.NodeBool)
		}
		to.SetBool(value.(bool))
		return true, nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if nodetype != syntax.NodeNumber {
			return true, newNodeErr(syntax.NodeNumber)
		}
		i, exact := constant.Int64Val(value.(constant.Value))
		if !exact {
			return true, fmt.Errorf("cannot assign %v to %v: value does not fit", value, to.Type())
		}
		to.SetInt(i)
		return true, nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if nodetype != syntax.NodeNumber {
			return true, newNodeErr(syntax.NodeNumber)
		}
		i, exact := constant.Uint64Val(value.(constant.Value))
		if !exact {
			return true, fmt.Errorf("cannot assign %v to %v: value does not fit", value, to.Type())
		}
		to.SetUint(i)
		return true, nil

	case reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		if nodetype != syntax.NodeNumber {
			return true, newNodeErr(syntax.NodeNumber)
		}
		switch constv := value.(type) {
		case constant.Value:
			f, _ := constant.Float64Val(constv)
			if kind == reflect.Complex64 || kind == reflect.Complex128 {
				to.SetComplex(complex(f, 0))
			} else {
				to.SetFloat(f)
			}
		case float64:
			if kind == reflect.Complex64 || kind == reflect.Complex128 {
				to.SetComplex(complex(constv, 0))
			} else {
				to.SetFloat(constv)
			}
		default:
			panic(fmt.Sprintf("unsupported node value %T for number node", constv))
		}
		return true, nil

	case reflect.String:
		if nodetype != syntax.NodeString {
			return true, newNodeErr(syntax.NodeString)
		}
		to.SetString(value.(string))
		return true, nil

	}

	return false, fmt.Errorf("%v is not a scalar", to.Type())
}

func ToScalar(val reflect.Value) reflect.Value {
	switch out := val.Interface().(type) {
	case bool:
		return reflect.ValueOf(&out).Elem()
	case string:
		return reflect.ValueOf(&out).Elem()
	case constant.Value:
		switch out.Kind() {
		case constant.Int:
			return ConstantToInt(out)
		case constant.Float:
			return ConstantToFloat(out)
		default:
			panic(fmt.Sprintf("unsupported constant kind %v for number node", out.Kind()))
		}
	case float64: // for infinites
		return reflect.ValueOf(&out).Elem()
	}
	return val
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

func IsList(typ reflect.Type) bool {
	return typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array
}

func IsMap(typ reflect.Type) bool {
	return typ.Kind() == reflect.Map || typ.Kind() == reflect.Struct
}
