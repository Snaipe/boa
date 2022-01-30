// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package reflectutil

import (
	"encoding"
	"fmt"
	"go/constant"
	"math"
	"math/big"
	"reflect"
	"sort"
	"unsafe"

	. "snai.pe/boa/encoding"
)

type Stringifier interface {
	Stringify(v reflect.Value) (string, bool, error)
}

type StructTagParser interface {
	ParseStructTag(tag reflect.StructTag) (string, bool)
}

type Marshaler interface {
	MarshalValue(v reflect.Value) (bool, error)
	MarshalList(v reflect.Value) (bool, error)
	MarshalListElem(l, v reflect.Value, i int) (bool, error)
	MarshalMap(v reflect.Value) (bool, error)
	MarshalMapKey(mv reflect.Value, k string) error
	MarshalMapValue(mv, v reflect.Value, k string, i int) (bool, error)

	MarshalBool(v bool) error
	MarshalString(v string) error
	MarshalNumber(v constant.Value) error
}

type PostListMarshaler interface {
	MarshalListPost(v reflect.Value) error
}

type PostListElemMarshaler interface {
	MarshalListElemPost(l, v reflect.Value, i int) error
}

type PostMapMarshaler interface {
	MarshalMapPost(v reflect.Value) error
}

type PostMapValueMarshaler interface {
	MarshalMapValuePost(mv, v reflect.Value, k string, i int) error
}

type NaNMarshaler interface {
	MarshalNaN(nan float64) error
}

type InfMarshaler interface {
	MarshalInf(inf float64) error
}

type NilMarshaler interface {
	MarshalNil() error
}

func Marshal(val reflect.Value, marshaler Marshaler, convention NamingConvention) error {
	typ := val.Type()

	if ok, err := marshaler.MarshalValue(val); ok || err != nil {
		return err
	}

	switch m := val.Interface().(type) {
	case *big.Float:
		return marshaler.MarshalNumber(constant.Make(m))
	case big.Float:
		return marshaler.MarshalNumber(constant.Make(&m))
	case *big.Rat:
		return marshaler.MarshalNumber(constant.Make(m))
	case big.Rat:
		return marshaler.MarshalNumber(constant.Make(&m))
	case *big.Int:
		return marshaler.MarshalNumber(constant.Make(m))
	case big.Int:
		return marshaler.MarshalNumber(constant.Make(&m))
	case encoding.TextMarshaler:
		txt, err := m.MarshalText()
		if err != nil {
			return err
		}
		return marshaler.MarshalString(BytesToString(txt))
	case []byte:
		return marshaler.MarshalString(BytesToString(m))
	}

	switch kind := val.Kind(); kind {
	case reflect.Interface:
		if val.Elem().Kind() == reflect.Invalid {
			// This is a nil interface and we therefore can't marshal it
			break
		}
		fallthrough
	case reflect.Ptr:
		if val.IsNil() {
			m, ok := marshaler.(NilMarshaler)
			if !ok {
				return fmt.Errorf("format cannot encode nil")
			}
			return m.MarshalNil()
		}
		return Marshal(val.Elem(), marshaler, convention)

	case reflect.Bool:
		return marshaler.MarshalBool(val.Bool())

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return marshaler.MarshalNumber(constant.MakeInt64(val.Int()))

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return marshaler.MarshalNumber(constant.MakeUint64(val.Uint()))

	case reflect.Float32, reflect.Float64:
		flt := val.Float()
		switch {
		case math.IsNaN(flt):
			m, ok := marshaler.(NaNMarshaler)
			if !ok {
				return fmt.Errorf("format cannot encode NaN")
			}
			return m.MarshalNaN(flt)
		case math.IsInf(flt, 0):
			m, ok := marshaler.(InfMarshaler)
			if !ok {
				return fmt.Errorf("format cannot encode infinity")
			}
			return m.MarshalInf(flt)
		}
		return marshaler.MarshalNumber(constant.MakeFloat64(flt))

	case reflect.String:
		return marshaler.MarshalString(val.String())

	case reflect.Array, reflect.Slice:
		if ok, err := marshaler.MarshalList(val); ok || err != nil {
			return err
		}

		l := val.Len()
		for i := 0; i < l; i++ {
			elem := val.Index(i)
			if ok, err := marshaler.MarshalListElem(val, elem, i); ok || err != nil {
				return err
			}
			if err := Marshal(elem, marshaler, convention); err != nil {
				return err
			}
			if post, ok := marshaler.(PostListElemMarshaler); ok {
				if err := post.MarshalListElemPost(val, elem, i); err != nil {
					return err
				}
			}
		}
		if post, ok := marshaler.(PostListMarshaler); ok {
			if err := post.MarshalListPost(val); err != nil {
				return err
			}
		}
		return nil

	case reflect.Map:
		if ok, err := marshaler.MarshalMap(val); ok || err != nil {
			return err
		}

		keys := make([]string, val.Len())
		for i, k := range val.MapKeys() {
			if stringifier, ok := marshaler.(Stringifier); ok {
				str, ok, err := stringifier.Stringify(k)
				if err != nil {
					return err
				}
				if ok {
					keys[i] = str
					continue
				}
			}

			switch kval := k.Interface().(type) {
			case encoding.TextMarshaler:
				var txt []byte
				txt, err := kval.MarshalText()
				if err == nil {
					keys[i] = string(txt)
				}
			case fmt.Stringer:
				keys[i] = kval.String()
			case string:
				keys[i] = kval
			default:
				return fmt.Errorf("unable to marshal %T as object key", kval)
			}
		}
		sort.Strings(keys)

		for i, k := range keys {
			if err := marshaler.MarshalMapKey(val, k); err != nil {
				return err
			}
			elem := val.MapIndex(reflect.ValueOf(k))
			if ok, err := marshaler.MarshalMapValue(val, elem, k, i); ok || err != nil {
				return err
			}
			if err := Marshal(elem, marshaler, convention); err != nil {
				return err
			}
			if post, ok := marshaler.(PostMapValueMarshaler); ok {
				if err := post.MarshalMapValuePost(val, elem, k, i); err != nil {
					return err
				}
			}
		}
		if post, ok := marshaler.(PostMapMarshaler); ok {
			if err := post.MarshalMapPost(val); err != nil {
				return err
			}
		}
		return nil

	case reflect.Struct:
		if ok, err := marshaler.MarshalMap(val); ok || err != nil {
			return err
		}
		l := typ.NumField()
		for i := 0; i < l; i++ {
			field := typ.Field(i)

			name := convention.Format(field.Name)
			if parser, ok := marshaler.(StructTagParser); ok {
				if tag, ok := parser.ParseStructTag(field.Tag); ok {
					name = tag
				}
			}
			if nametag, ok := LookupTag(field.Tag, "name", false); ok {
				name = nametag.Value
			}

			if err := marshaler.MarshalMapKey(val, name); err != nil {
				return err
			}
			elem := val.FieldByIndex(field.Index)
			if ok, err := marshaler.MarshalMapValue(val, elem, name, i); ok || err != nil {
				return err
			}
			if err := Marshal(elem, marshaler, convention); err != nil {
				return err
			}
			if post, ok := marshaler.(PostMapValueMarshaler); ok {
				if err := post.MarshalMapValuePost(val, elem, name, i); err != nil {
					return err
				}
			}
		}
		if post, ok := marshaler.(PostMapMarshaler); ok {
			if err := post.MarshalMapPost(val); err != nil {
				return err
			}
		}
		return nil
	}

	return fmt.Errorf("cannot marshal %v: unsupported type %v", val.Interface(), typ)
}

// BytesToString converts the provided byte slice into a string. No copy is performed,
// which means that changing the data slice will also change the string, which will
// break any code that relies on the assumption that strings are read-only. Use
// with care.
func BytesToString(data []byte) string {
	return *(*string)(unsafe.Pointer(&data))
}
