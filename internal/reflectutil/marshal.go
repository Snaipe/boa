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
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unsafe"

	. "snai.pe/boa/encoding"
)

type FieldOpts struct {
	Name   string
	Help   []string
	Ignore bool
	Naming NamingConvention
	Inline bool
	Env    string
}

type MapEntry struct {
	Key     string
	Value   reflect.Value
	Options FieldOpts
}

type Stringifier interface {
	Stringify(v reflect.Value) (string, bool, error)
}

type StructTagParser interface {
	ParseStructTag(tag reflect.StructTag) (FieldOpts, bool)
}

type Marshaler interface {
	MarshalValue(v reflect.Value) (bool, error)
	MarshalList(v reflect.Value) (bool, error)
	MarshalListElem(l, v reflect.Value, i int) (bool, error)
	MarshalMap(v reflect.Value, kvs []MapEntry) (bool, error)
	MarshalMapKey(mv reflect.Value, kv MapEntry, i int) error
	MarshalMapValue(mv reflect.Value, kv MapEntry, i int) (bool, error)

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
	MarshalMapPost(v reflect.Value, kvs []MapEntry) error
}

type PostMapValueMarshaler interface {
	MarshalMapValuePost(mv reflect.Value, kv MapEntry, i int) error
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

	nilFallback := func() error {
		return fmt.Errorf("format cannot encode nil")
	}
	switch m := val.Interface().(type) {
	case *big.Float:
		if !val.IsNil() {
			return marshaler.MarshalNumber(constant.Make(m))
		}
		nilFallback = func() error {
			return marshaler.MarshalNumber(constant.MakeFloat64(0.0))
		}
	case big.Float:
		return marshaler.MarshalNumber(constant.Make(&m))
	case *big.Rat:
		if !val.IsNil() {
			return marshaler.MarshalNumber(constant.Make(m))
		}
		nilFallback = func() error {
			return marshaler.MarshalNumber(constant.MakeFloat64(0.0))
		}
	case big.Rat:
		return marshaler.MarshalNumber(constant.Make(&m))
	case *big.Int:
		if !val.IsNil() {
			return marshaler.MarshalNumber(constant.Make(m))
		}
		nilFallback = func() error {
			return marshaler.MarshalNumber(constant.MakeInt64(0))
		}
	case big.Int:
		return marshaler.MarshalNumber(constant.Make(&m))
	case encoding.TextMarshaler:
		if !(val.Kind() == reflect.Interface || val.Kind() == reflect.Pointer) || !val.IsNil() {
			txt, err := m.MarshalText()
			if err != nil {
				return err
			}
			return marshaler.MarshalString(BytesToString(txt))
		}
	case []byte:
		return marshaler.MarshalString(BytesToString(m))
	case *url.URL:
		if !val.IsNil() {
			return marshaler.MarshalString(m.String())
		}
		nilFallback = func() error {
			return marshaler.MarshalString("")
		}
	case url.URL:
		return marshaler.MarshalString(m.String())
	case *regexp.Regexp:
		if m != nil {
			return marshaler.MarshalString(m.String())
		}
	case regexp.Regexp:
		return marshaler.MarshalString(m.String())
	}

	switch kind := val.Kind(); kind {
	case reflect.Interface, reflect.Ptr:
		if val.IsNil() {
			m, ok := marshaler.(NilMarshaler)
			if !ok {
				return nilFallback()
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
		kvs := make([]MapEntry, val.Len())
		for i, k := range val.MapKeys() {
			v := val.MapIndex(k)
			for v.Kind() == reflect.Interface && !v.IsNil() {
				v = v.Elem()
			}
			if stringifier, ok := marshaler.(Stringifier); ok {
				str, ok, err := stringifier.Stringify(k)
				if err != nil {
					return err
				}
				if ok {
					kvs[i] = MapEntry{Key: str, Value: v}
					continue
				}
			}

			switch kval := k.Interface().(type) {
			case encoding.TextMarshaler:
				var txt []byte
				txt, err := kval.MarshalText()
				if err != nil {
					return err
				}
				kvs[i] = MapEntry{Key: string(txt), Value: v}
			case string:
				kvs[i] = MapEntry{Key: kval, Value: v}
			default:
				return fmt.Errorf("unable to marshal %T as object key", kval)
			}
		}
		sort.Slice(kvs, func(i, j int) bool { return kvs[i].Key < kvs[j].Key })

		if ok, err := marshaler.MarshalMap(val, kvs); ok || err != nil {
			return err
		}

		for i, kv := range kvs {
			// If the value is nil, and we can't represent nil in the format,
			// just omit the field entirely. There isn't much else sensible we can do.
			if (kv.Value.Kind() == reflect.Ptr || kv.Value.Kind() == reflect.Interface) && kv.Value.IsNil() {
				if _, ok := marshaler.(NilMarshaler); !ok {
					continue
				}
			}

			if err := marshaler.MarshalMapKey(val, kv, i); err != nil {
				return err
			}
			if ok, err := marshaler.MarshalMapValue(val, kv, i); err != nil {
				return err
			} else if ok {
				continue
			}
			if err := Marshal(kv.Value, marshaler, convention); err != nil {
				return err
			}
			if post, ok := marshaler.(PostMapValueMarshaler); ok {
				if err := post.MarshalMapValuePost(val, kv, i); err != nil {
					return err
				}
			}
		}
		if post, ok := marshaler.(PostMapMarshaler); ok {
			if err := post.MarshalMapPost(val, kvs); err != nil {
				return err
			}
		}
		return nil

	case reflect.Struct:
		kvs := VisibleFieldsAsMapEntries(val, convention, marshaler)

		if ok, err := marshaler.MarshalMap(val, kvs); ok || err != nil {
			return err
		}

		for i, kv := range kvs {
			// If the value is nil, and we can't represent nil in the format,
			// just omit the field entirely. There isn't much else sensible we can do.
			if (kv.Value.Kind() == reflect.Ptr || kv.Value.Kind() == reflect.Interface) && kv.Value.IsNil() {
				if _, ok := marshaler.(NilMarshaler); !ok {
					continue
				}
			}

			if err := marshaler.MarshalMapKey(val, kv, i); err != nil {
				return err
			}
			if ok, err := marshaler.MarshalMapValue(val, kv, i); err != nil {
				return err
			} else if ok {
				continue
			}
			if err := Marshal(kv.Value, marshaler, kv.Options.Naming); err != nil {
				return err
			}
			if post, ok := marshaler.(PostMapValueMarshaler); ok {
				if err := post.MarshalMapValuePost(val, kv, i); err != nil {
					return err
				}
			}
		}
		if post, ok := marshaler.(PostMapMarshaler); ok {
			if err := post.MarshalMapPost(val, kvs); err != nil {
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

// IsValueType returns true if the specified type can be represented as a
// single value.
func IsValueType(t reflect.Type) bool {
	if t.Implements(reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()) {
		return true
	}
	switch t {
	case reflect.TypeOf(big.Int{}),
		reflect.TypeOf(big.Float{}),
		reflect.TypeOf(big.Rat{}),
		reflect.TypeOf(url.URL{}),
		reflect.TypeOf(regexp.Regexp{}),
		reflect.TypeOf([]byte(nil)):
		return true
	}
	return false
}

// Len returns the "length" of the specified value.
// If v is a map, slice, or array, it returns its length
// If v is a struct, it returns the number of fields
// For everything else, Len returns 1.
func Len(v reflect.Value) int {
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		return v.Len()
	case reflect.Struct:
		return v.NumField()
	default:
		return 1
	}
}

// ParseFieldOpts parses the standard set of field options that boa supports.
func ParseFieldOpts(tag reflect.StructTag, marshaler interface{}, convention NamingConvention) (opts FieldOpts) {
	if parser, ok := marshaler.(StructTagParser); ok {
		opts, _ = parser.ParseStructTag(tag)
	}
	if _, ok := LookupTag(tag, "-", false); ok {
		opts.Ignore = true
	}
	if opts.Ignore {
		return
	}
	if nametag, ok := LookupTag(tag, "name", false); ok {
		opts.Name = nametag.Value
	}
	if helptag, ok := LookupTag(tag, "help", false); ok {
		help := strings.TrimSpace(helptag.Value)
		if help != "" {
			lines := strings.Split(help, "\n")
			for i, l := range lines {
				lines[i] = strings.TrimSpace(l)
			}
			opts.Help = lines
		}
	}
	if naming, ok := LookupTag(tag, "naming", false); ok {
		conv := NamingConventionByName(naming.Value)
		if conv == nil {
			panic(fmt.Sprintf("unknown naming convention %s", naming.Value))
		}
		opts.Naming = conv
	}
	_, opts.Inline = LookupTag(tag, "inline", false)
	if opts.Naming == nil {
		opts.Naming = convention
	}
	if env, ok := LookupTag(tag, "env", false); ok {
		opts.Env = env.Value
	}
	return
}
