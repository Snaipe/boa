// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package reflectutil

import (
	"fmt"
	"reflect"
)

func DeepEqual(lhs, rhs interface{}) error {
	return deepEqual(reflect.ValueOf(lhs), reflect.ValueOf(rhs))
}

func deepEqual(lhs, rhs reflect.Value) error {
	for lhs.Kind() == reflect.Ptr || lhs.Kind() == reflect.Ptr {
		if lhs.IsNil() {
			break
		}
		lhs = lhs.Elem()
	}
	for rhs.Kind() == reflect.Ptr || rhs.Kind() == reflect.Ptr {
		if rhs.IsNil() {
			break
		}
		rhs = lhs.Elem()
	}
	if lhs.Kind() != rhs.Kind() {
		return fmt.Errorf("mismatching kinds %v and %v", lhs.Kind(), rhs.Kind())
	}

	switch lhs.Kind() {
	case reflect.Ptr, reflect.Interface:
		if lhs.IsNil() != rhs.IsNil() {
			return fmt.Errorf("one value is nil while the other isn't")
		}
		if lhs.IsNil() {
			return nil
		}
		return deepEqual(lhs.Elem(), rhs.Elem())
	case reflect.Array, reflect.Slice:
		if lhs.Len() != rhs.Len() {
			return fmt.Errorf("one value has length %d while the other has length %d", lhs.Len(), rhs.Len())
		}
		for i := 0; i < lhs.Len(); i++ {
			if err := deepEqual(lhs.Index(i), rhs.Index(i)); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
	case reflect.Map:
		if lhs.Len() != rhs.Len() {
			return fmt.Errorf("one value has length %d while the other has length %d", lhs.Len(), rhs.Len())
		}
		tryKey := func(k, to reflect.Value) reflect.Value {
			for k.Kind() == reflect.Interface {
				k = k.Elem()
			}
			return to.MapIndex(k)
		}
		for _, k := range lhs.MapKeys() {
			if err := deepEqual(tryKey(k, lhs), tryKey(k, rhs)); err != nil {
				return fmt.Errorf("[%v]: %w", k, err)
			}
		}
	case reflect.Struct:
		if lhs.Type() != rhs.Type() {
			return fmt.Errorf("mismatching types %v and %v", lhs.Type(), rhs.Type())
		}
		for i := 0; i < lhs.NumField(); i++ {
			if err := deepEqual(lhs.Field(i), rhs.Field(i)); err != nil {
				field := lhs.Type().Field(i)
				return fmt.Errorf(".%v: %w", field.Name, err)
			}
		}

	case reflect.Func:
		if !lhs.IsNil() || !rhs.IsNil() {
			return fmt.Errorf("non-nil functions are not comparable")
		}
	case reflect.Bool:
		if lhs.Bool() != rhs.Bool() {
			return fmt.Errorf("values not equal")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if lhs.Int() != rhs.Int() {
			return fmt.Errorf("values not equal")
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if lhs.Uint() != rhs.Uint() {
			return fmt.Errorf("values not equal")
		}
	case reflect.Float32, reflect.Float64:
		if lhs.Float() != rhs.Float() {
			return fmt.Errorf("values not equal")
		}
	case reflect.Complex64, reflect.Complex128:
		if lhs.Complex() != rhs.Complex() {
			return fmt.Errorf("values not equal")
		}
	case reflect.String:
		if lhs.String() != rhs.String() {
			return fmt.Errorf("values not equal")
		}
	default:
		return fmt.Errorf("uncomparable value %v", lhs.Kind())
	}
	return nil
}
