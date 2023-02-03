// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package reflectutil

import (
	stdenc "encoding"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"snai.pe/boa/encoding"
)

func UnmarshalText(to reflect.Value, value string) (bool, error) {

	if to.CanAddr() {
		switch ptr := to.Addr().Interface().(type) {
		case stdenc.TextUnmarshaler:
			return true, ptr.UnmarshalText([]byte(value))
		}
	}

	switch to.Interface().(type) {
	case []byte:
		to.Set(reflect.ValueOf([]byte(value)))
		return true, nil
	}

	switch kind := to.Kind(); kind {

	case reflect.Ptr:
		ptr := reflect.New(to.Type().Elem())
		ok, err := UnmarshalText(ptr, value)
		if ok && err == nil {
			to.Set(ptr)
		}
		return ok, err

	case reflect.Bool:
		v, err := strconv.ParseBool(value)
		if err == nil {
			to.SetBool(v)
		}
		return true, err

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(value, 0, 64)
		if err == nil {
			to.SetInt(v)
		}
		return true, err

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		v, err := strconv.ParseUint(value, 0, 64)
		if err == nil {
			to.SetUint(v)
		}
		return true, err

	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(value, 64)
		if err == nil {
			to.SetFloat(v)
		}
		return true, err

	case reflect.Complex64, reflect.Complex128:
		v, err := strconv.ParseComplex(value, 128)
		if err == nil {
			to.SetComplex(v)
		}
		return true, err

	case reflect.String:
		to.SetString(value)
		return true, nil

	case reflect.Interface:
		to.Set(reflect.ValueOf(value))
		return true, nil

	}

	return false, nil
}

func PopulateFromEnv(to reflect.Value, automatic bool, names []string, lookup func(string) (string, bool)) (bool, error) {

	if len(names) == 0 {
		return true, nil
	}

	if to.Kind() == reflect.Ptr {
		if !to.IsNil() {
			return PopulateFromEnv(to.Elem(), automatic, names, lookup)
		}

		ptr := reflect.New(to.Type().Elem())
		ok, err := PopulateFromEnv(ptr.Elem(), automatic, names, lookup)
		if ok && err == nil {
			to.Set(ptr)
		}
		return ok, err
	}

	var (
		value   string
		defined bool
	)
	for i := 0; i < len(names) && !defined; i++ {
		value, defined = lookup(names[i])
	}

	if automatic && defined {
		if ok, err := UnmarshalText(to, value); ok {
			return true, err
		}
	}

	for to.Kind() == reflect.Interface {
		to = to.Elem()
	}

	switch kind := to.Kind(); kind {

	case reflect.Slice, reflect.Array:
		if defined {
			list := strings.Split(value, string(os.PathListSeparator))
			length := to.Len()
			if len(list) > length && kind == reflect.Slice {
				length = len(list)
				to.Set(reflect.MakeSlice(to.Type(), length, length))
			}
			for i := 0; i < length; i++ {
				ok, err := UnmarshalText(to.Index(i), list[i])
				if !ok && err == nil {
					err = fmt.Errorf("cannot set element %d on list: %v cannot be populated from %q", i, to.Index(i).Type(), list[i])
				}
				if err != nil {
					return false, err
				}
			}
		}

	case reflect.Map:
		for _, k := range to.MapKeys() {
			var key string
			switch kval := k.Interface().(type) {
			case encoding.TextMarshaler:
				var txt []byte
				txt, err := kval.MarshalText()
				if err != nil {
					return false, err
				}
				key = string(txt)
			case string:
				key = kval
			default:
				// Unable to stringify map key; skip environment population
				continue
			}

			elem := to.MapIndex(k)
			ptr := reflect.New(elem.Type())
			ptr.Elem().Set(elem)
			elem = ptr.Elem()

			toEnv := func(r rune) rune {
				if !unicode.In(r, unicode.Letter, unicode.Digit) {
					return '_'
				}
				return unicode.ToUpper(r)
			}

			formatted := encoding.ScreamingSnakeCase.Format(key)
			asEnv := strings.Map(toEnv, key)

			uniq := map[string]struct{}{}
			for _, name := range names {
				uniq[name + "_" + formatted] = struct{}{}
				uniq[name + "_" + asEnv] = struct{}{}
			}
			tentatives := make([]string, 0, len(uniq))
			for name := range uniq {
				tentatives = append(tentatives, name)
			}
			ok, err := PopulateFromEnv(elem, automatic, tentatives, lookup)
			if err != nil {
				return ok, err
			}
			if ok {
				to.SetMapIndex(k, elem)
			}
		}

	case reflect.Struct:
		changed := false
		fields, _ := VisibleFields(to, encoding.ScreamingSnakeCase, nil)
		for _, field := range fields {
			var (
				ok  bool
				err error
			)
			if field.Options.Env != "" {
				ok, err = PopulateFromEnv(field.Value, true, []string{field.Options.Env}, lookup)
			} else {
				key := encoding.ScreamingSnakeCase.Format(field.Name)
				uniq := map[string]struct{}{}
				for _, name := range names {
					uniq[name + "_" + key] = struct{}{}
				}
				tentatives := make([]string, 0, len(uniq))
				for name := range uniq {
					tentatives = append(tentatives, name)
				}
				ok, err = PopulateFromEnv(field.Value, automatic, tentatives, lookup)
			}
			changed = changed || ok
			if err != nil {
				return changed, err
			}
		}
		return changed, nil
	}

	return false, nil
}
