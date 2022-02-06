// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package reflectutil

import (
	"reflect"

	"snai.pe/boa/encoding"
)

type StructField struct {
	reflect.StructField
	Value   reflect.Value
	Options FieldOpts
}

func VisibleFields(val reflect.Value, convention encoding.NamingConvention, unmarshaler interface{}) ([]StructField, map[string]StructField) {
	t := val.Type()
	fields := make(map[string]StructField, t.NumField()*2)
	order := make([]StructField, 0, t.NumField())
	order = walkFields(fields, order, nil, val, val.Type(), convention, unmarshaler)

	// Remove fields with nil indices -- they've been shadowed.
	l := 0
	for _, field := range order {
		if field.Index == nil {
			continue
		}
		order[l] = field
		l++
	}

	return order[:l], fields
}

func walkFields(fields map[string]StructField, order []StructField, index []int, val reflect.Value, typ reflect.Type, convention encoding.NamingConvention, unmarshaler interface{}) []StructField {
	for i := 0; i < typ.NumField(); i++ {
		var (
			field = typ.Field(i)
			elem  = val.Field(i)
			idx   = append(index, i)
		)

		// Ignore private fields
		if field.PkgPath != "" {
			continue
		}

		opts := ParseFieldOpts(field.Tag, unmarshaler, convention)
		if opts.Ignore {
			continue
		}

		if field.Anonymous && opts.Name == "" || opts.Inline {
			order = walkFields(fields, order, idx, elem, field.Type, opts.Naming, unmarshaler)
			continue
		}
		if opts.Name == "" {
			opts.Name = opts.Naming.Format(field.Name)
		}

		if existing, ok := fields[opts.Name]; ok {
			switch {
			case len(existing.Index) == len(idx):
				existing.Index = nil
				continue
			case len(existing.Index) > len(idx):
				existing.Index = nil
			default:
				continue
			}
		}

		// Override the field Index so that v.FieldByIndex() works with the top-level value.
		field.Index = append([]int(nil), idx...)
		structfield := StructField{field, elem, opts}
		fields[opts.Name] = structfield
		order = append(order, structfield)
	}
	return order
}

func VisibleFieldsAsMapEntries(val reflect.Value, convention encoding.NamingConvention, marshaler interface{}) []MapEntry {
	fields, _ := VisibleFields(val, convention, marshaler)
	entries := make([]MapEntry, len(fields))
	for i, field := range fields {
		entries[i] = MapEntry{Key: field.Options.Name, Value: field.Value, Options: field.Options}
	}
	return entries
}
