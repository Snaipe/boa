// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package reflectutil

import (
	"reflect"
	"sync"
	"unsafe"

	"snai.pe/boa/encoding"
)

type StructField struct {
	reflect.StructField
	Value   reflect.Value
	Options FieldOpts
}

// layoutField holds the type-level field metadata that can be cached
// independently of any particular reflect.Value.
type layoutField struct {
	reflect.StructField
	Options FieldOpts
}

// structLayout is the cached result of walking a struct type's fields.
type structLayout struct {
	fields []layoutField
	byName map[string]int // name -> index into fields
}

type layoutKey struct {
	typ         reflect.Type
	convention  unsafe.Pointer // data pointer of the convention interface; nil if none
	unmarshaler reflect.Type
}

var layoutCache sync.Map // layoutKey -> *structLayout

func makeLayoutKey(typ reflect.Type, convention encoding.NamingConvention, unmarshaler interface{}) layoutKey {
	var conventionPtr unsafe.Pointer
	if convention != nil {
		// Conventions are singletons. For non-pointer types stored in interfaces,
		// Go heap-allocates the value and stores a pointer to it in the interface
		// data word. All copies of the same convention share that pointer, making
		// it a stable identity key with no allocation.
		conventionPtr = (*[2]unsafe.Pointer)(unsafe.Pointer(&convention))[1]
	}
	var unmarshalerType reflect.Type
	if unmarshaler != nil {
		unmarshalerType = reflect.TypeOf(unmarshaler)
	}
	return layoutKey{typ, conventionPtr, unmarshalerType}
}

func getLayout(typ reflect.Type, convention encoding.NamingConvention, unmarshaler interface{}) *structLayout {
	key := makeLayoutKey(typ, convention, unmarshaler)
	if v, ok := layoutCache.Load(key); ok {
		return v.(*structLayout)
	}

	// Compute layout from type information only.
	fields := make(map[string]layoutField, typ.NumField()*2)
	order := make([]layoutField, 0, typ.NumField())
	order = walkFields(fields, order, nil, typ, convention, unmarshaler)

	// Remove shadowed fields.
	l := 0
	for _, f := range order {
		if f.Index == nil {
			continue
		}
		order[l] = f
		l++
	}
	order = order[:l]

	byName := make(map[string]int, len(order))
	for i := range order {
		byName[order[i].Options.Name] = i
	}
	layout := &structLayout{fields: order, byName: byName}
	v, _ := layoutCache.LoadOrStore(key, layout)
	return v.(*structLayout)
}

func walkFields(fields map[string]layoutField, order []layoutField, index []int, typ reflect.Type, convention encoding.NamingConvention, unmarshaler interface{}) []layoutField {
	for i := 0; i < typ.NumField(); i++ {
		var (
			field = typ.Field(i)
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
			order = walkFields(fields, order, idx, field.Type, opts.Naming, unmarshaler)
			continue
		}
		if opts.Name == "" && opts.Naming != nil {
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
		lf := layoutField{StructField: field, Options: opts}
		fields[opts.Name] = lf
		order = append(order, lf)
	}
	return order
}

func VisibleFields(val reflect.Value, convention encoding.NamingConvention, unmarshaler interface{}) ([]StructField, map[string]StructField) {
	layout := getLayout(val.Type(), convention, unmarshaler)

	order := make([]StructField, len(layout.fields))
	byName := make(map[string]StructField, len(layout.fields))
	for i := range layout.fields {
		lf := &layout.fields[i]
		sf := StructField{
			StructField: lf.StructField,
			Value:       val.FieldByIndex(lf.Index),
			Options:     lf.Options,
		}
		order[i] = sf
		byName[lf.Options.Name] = sf
	}
	return order, byName
}

// LookupField returns the StructField for the given serialized name without
// allocating a full field map. It is the preferred hot path for unmarshalers
// that perform per-key lookups rather than full iteration.
func LookupField(val reflect.Value, convention encoding.NamingConvention, unmarshaler interface{}, name string) (StructField, bool) {
	layout := getLayout(val.Type(), convention, unmarshaler)
	i, ok := layout.byName[name]
	if !ok {
		return StructField{}, false
	}
	lf := &layout.fields[i]
	return StructField{
		StructField: lf.StructField,
		Value:       val.FieldByIndex(lf.Index),
		Options:     lf.Options,
	}, true
}

func VisibleFieldsAsMapEntries(val reflect.Value, convention encoding.NamingConvention, marshaler interface{}) []MapEntry {
	layout := getLayout(val.Type(), convention, marshaler)
	entries := make([]MapEntry, len(layout.fields))
	for i := range layout.fields {
		lf := &layout.fields[i]
		entries[i] = MapEntry{Key: lf.Options.Name, Value: val.FieldByIndex(lf.Index), Options: lf.Options}
	}
	return entries
}
