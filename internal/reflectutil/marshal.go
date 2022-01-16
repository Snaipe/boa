// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package reflectutil

import (
	"encoding"
	"fmt"
	"reflect"
	"sort"
)

type Stringifier interface {
	Stringify(v reflect.Value) (string, bool, error)
}

type StructTagParser interface {
	ParseStructTag(tag reflect.StructTag) (string, bool)
}

type Marshaler interface{
	MarshalValue(v reflect.Value) (bool, error)
	MarshalString(v reflect.Value) error
	MarshalList(v reflect.Value) (bool, error)
	MarshalListElem(l, v reflect.Value, i int) (bool, error)
	MarshalMap(v reflect.Value) (bool, error)
	MarshalMapKey(mv reflect.Value, k string) error
	MarshalMapValue(mv, v reflect.Value, k string, i int) (bool, error)
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

func Marshal(val reflect.Value, marshaler Marshaler) error {
	typ := val.Type()

	if ok, err := marshaler.MarshalValue(val); ok || err != nil {
		return err
	}

	switch m := val.Interface().(type) {
	case encoding.TextMarshaler:
		txt, err := m.MarshalText()
		if err != nil {
			return err
		}
		if err := marshaler.MarshalString(reflect.ValueOf(txt)); err != nil {
			return err
		}
	case []byte:
		if err := marshaler.MarshalString(val); err != nil {
			return err
		}
	}

	switch kind := val.Kind(); kind {
	case reflect.String:
		if err := marshaler.MarshalString(val); err != nil {
			return err
		}

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
			if err := Marshal(elem, marshaler); err != nil {
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
			if err := Marshal(elem, marshaler); err != nil {
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

	case reflect.Struct:
		if ok, err := marshaler.MarshalMap(val); ok || err != nil {
			return err
		}
		l := typ.NumField()
		for i := 0; i < l; i++ {
			field := typ.Field(i)

			name := field.Name
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
			if err := Marshal(elem, marshaler); err != nil {
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

	default:
		return fmt.Errorf("cannot marshal %v: unsupported type %v", val.Interface(), typ)
	}

	return nil
}
