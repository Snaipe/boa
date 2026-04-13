// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package yaml

import (
	"fmt"
	"io"
	"os"
	"reflect"

	. "snai.pe/boa/syntax"

	"snai.pe/boa/encoding"
	"snai.pe/boa/internal/encutil"
	"snai.pe/boa/internal/reflectutil"
)

type structTagParser struct{}

func (structTagParser) ParseStructTag(tag reflect.StructTag) (reflectutil.FieldOpts, bool) {
	var opts reflectutil.FieldOpts
	if yamltag, ok := reflectutil.LookupTag(tag, "yaml", true); ok {
		if yamltag.Value == "-" {
			opts.Ignore = true
		} else {
			opts.Name = yamltag.Value
		}
		return opts, true
	}
	return opts, false
}

type unmarshaler struct {
	encutil.UnmarshalerBase
	structTagParser
	schema *Schema
}

func (*unmarshaler) UnmarshalValue(val reflect.Value, node Value) (bool, error) {
	// A Merge node in value position (e.g. "foo: <<") decodes as the literal
	// string "<<" rather than producing an error.
	if _, ok := node.(*Merge); ok {
		switch val.Kind() {
		case reflect.Interface:
			val.Set(reflect.ValueOf("<<"))
			return true, nil
		case reflect.String:
			val.SetString("<<")
			return true, nil
		}
	}
	return false, nil
}

// PreprocessMap implements reflectutil.MapPreprocessor by expanding YAML merge
// keys (<<) before the generic map-unmarshaling logic processes the entries.
func (*unmarshaler) PreprocessMap(mapNode *Map) (*Map, error) {
	return flattenMerge(mapNode)
}

// flattenMerge rewrites mapNode so that all merge key (<<) entries are
// expanded inline. Explicit entries appear first in their original order;
// merged entries whose keys do not already appear explicitly follow.
// Earlier merge sources take precedence over later ones.
// Returns mapNode unchanged if it contains no merge keys.
func flattenMerge(mapNode *Map) (*Map, error) {
	hasMerge := false
	for _, entry := range mapNode.Entries {
		if _, ok := entry.Key.(*Merge); ok {
			hasMerge = true
			break
		}
	}
	if !hasMerge {
		return mapNode, nil
	}

	// Build the set of keys that appear explicitly (non-merge) in this mapping.
	explicitKeys := make(map[string]bool)
	for _, entry := range mapNode.Entries {
		if _, ok := entry.Key.(*Merge); ok {
			continue
		}
		if s, ok := resolveAlias(entry.Key).(*String); ok {
			explicitKeys[s.Value] = true
		}
	}

	// Produce the flat entry list: explicit entries first, merged entries second.
	result := &Map{Node: mapNode.Node}
	for _, entry := range mapNode.Entries {
		if _, ok := entry.Key.(*Merge); ok {
			continue
		}
		result.Entries = append(result.Entries, entry)
	}
	for _, entry := range mapNode.Entries {
		if _, ok := entry.Key.(*Merge); ok {
			if err := appendMergeEntries(&result.Entries, entry.Value, explicitKeys); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

// appendMergeEntries recursively appends entries from mergeVal (a mapping or
// sequence of mappings) to entries, skipping keys already present in skipKeys.
// skipKeys is extended as entries are added to prevent later sources from
// overriding earlier ones.
func appendMergeEntries(entries *[]*MapEntry, mergeVal Value, skipKeys map[string]bool) error {
	mergeVal = resolveAlias(mergeVal)
	switch v := mergeVal.(type) {
	case *Map:
		for _, entry := range v.Entries {
			if _, ok := entry.Key.(*Merge); ok {
				// Nested merge key inside a merged mapping: recurse.
				if err := appendMergeEntries(entries, entry.Value, skipKeys); err != nil {
					return err
				}
				continue
			}
			s, ok := resolveAlias(entry.Key).(*String)
			if !ok {
				continue // non-string keys cannot participate in a merge
			}
			if skipKeys[s.Value] {
				continue // already set by explicit or earlier-merged entry
			}
			skipKeys[s.Value] = true
			*entries = append(*entries, entry)
		}
		return nil
	case *List:
		for _, item := range v.Items {
			if err := appendMergeEntries(entries, item, skipKeys); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("merge key value must be a mapping or sequence of mappings, got %T", mergeVal)
	}
}

// resolveAlias follows alias chains and returns the first non-alias value.
func resolveAlias(v Value) Value {
	for {
		if a, ok := v.(*Alias); ok {
			v = a.Target
		} else {
			return v
		}
	}
}

var (
	_ reflectutil.StructTagParser = (*unmarshaler)(nil)
	_ reflectutil.Unmarshaler     = (*unmarshaler)(nil)
	_ reflectutil.MapPreprocessor = (*unmarshaler)(nil)
)

type decoder struct {
	in          io.Reader
	unmarshaler unmarshaler
}

func NewDecoder(rd io.Reader) encoding.Decoder {
	var decoder decoder
	decoder.in = rd
	decoder.unmarshaler.NewParser = newParser
	decoder.unmarshaler.Self = &decoder.unmarshaler
	decoder.unmarshaler.Extensions = []string{".yaml", ".yml"}

	// Defaults
	decoder.unmarshaler.NamingConvention = encoding.KebabCase
	decoder.unmarshaler.schema = DefaultSchema
	return &decoder
}

func (decoder *decoder) Option(opts ...interface{}) encoding.Decoder {
	if err := decoder.unmarshaler.Option(opts...); err != nil {
		panic(err)
	}
	return decoder
}

func (decoder *decoder) Decode(v interface{}) error {
	return decoder.unmarshaler.Decode(decoder.in, v)
}

// Load is a convenience function to load a YAML document into the value
// pointed at by v. It is functionally equivalent to NewDecoder(<file at path>).Decode(v).
func Load(path string, v interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return NewDecoder(f).Decode(v)
}
