// Copyright 2026 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package encutil

import (
	"reflect"

	"snai.pe/boa/internal/reflectutil"
)

// StructTagParser implements reflectutil.StructTagParser for a single
// format-specific struct tag key (e.g. "json", "toml", "yaml").
// A value of "-" marks the field as ignored; any other value sets the
// field name override.
type StructTagParser struct {
	Tag string
}

func (p StructTagParser) ParseStructTag(tag reflect.StructTag) (reflectutil.FieldOpts, bool) {
	var opts reflectutil.FieldOpts
	if t, ok := reflectutil.LookupTag(tag, p.Tag, true); ok {
		if t.Value == "-" {
			opts.Ignore = true
		} else {
			opts.Name = t.Value
		}
		return opts, true
	}
	return opts, false
}
