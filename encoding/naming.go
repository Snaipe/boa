// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package encoding

import (
	"strings"
	"unicode"
)

// A NamingConvention formats a string that follows the Go naming convention
// into another naming convention.
type NamingConvention interface {
	Format(string) string
}

var (
	CamelCase          NamingConvention = camelCase("camelCase", 0, false)
	PascalCase         NamingConvention = camelCase("PascalCase", 0, true)
	SnakeCase          NamingConvention = constantCase("snake_case", '_', false)
	ScreamingSnakeCase NamingConvention = constantCase("SCREAMING_SNAKE_CASE", '_', true)
	KebabCase          NamingConvention = constantCase("kebab-case", '-', false)
	ScreamingKebabCase NamingConvention = constantCase("SCREAMING-KEBAB-CASE", '-', true)
	CamelSnakeCase     NamingConvention = camelCase("camel_Snake_Case", '_', false)
	PascalSnakeCase    NamingConvention = camelCase("Pascal_Snake_Case", '_', true)
	TrainCase          NamingConvention = camelCase("Train-Case", '-', true)
	FlatCase           NamingConvention = constantCase("flatcase", 0, false)
	UpperFlatCase      NamingConvention = constantCase("UPPERFLATCASE", 0, true)
)

type baseConvention struct {
	name string
	fn   func(rune, int, bool, bool, bool) (rune, rune)
}

func (bc baseConvention) Format(s string) string {
	in := strings.NewReader(s)

	var out strings.Builder

	var (
		prev   rune
		upper  bool // are we reading a FULL CAPS word?
		wupper bool // did we write an uppercase rune at word boundary?
		i      int
	)
	for {
		r, w, err := in.ReadRune()
		if err != nil {
			break
		}
		next, _, err := in.ReadRune()
		if err == nil {
			in.UnreadRune()
		} else {
			next = r
		}

		var boundary bool
		switch {
		case i == 0:
			boundary = true
		case unicode.IsDigit(r) != unicode.IsDigit(prev):
			boundary = true
		case unicode.IsLower(prev) && unicode.IsUpper(r):
			boundary = true
		case unicode.IsUpper(r) && unicode.IsLower(next):
			boundary = true
		}
		upper = unicode.IsUpper(r) && unicode.IsUpper(prev)

		tr, sep := bc.fn(r, i, boundary, upper, wupper)
		if sep != 0 {
			out.WriteRune(sep)
		}
		out.WriteRune(tr)
		i += w
		prev = r

		if boundary {
			wupper = unicode.IsUpper(tr)
		}
	}
	return out.String()
}

func (bc baseConvention) String() string {
	return bc.name
}

func camelCase(name string, sep rune, capitalizeFirst bool) baseConvention {
	runefunc := func(r rune, i int, boundary, upper, wupper bool) (rune, rune) {
		fn := unicode.ToLower
		if (upper && wupper) || boundary && (capitalizeFirst || i != 0) {
			fn = unicode.ToUpper
		}
		var sc rune
		if boundary && i != 0 {
			sc = sep
		}
		return fn(r), sc
	}
	return baseConvention{name, runefunc}
}

func constantCase(name string, sep rune, screaming bool) baseConvention {
	fn := unicode.ToLower
	if screaming {
		fn = unicode.ToUpper
	}
	runefunc := func(r rune, i int, boundary, upper, wupper bool) (rune, rune) {
		var sc rune
		if boundary && i != 0 {
			sc = sep
		}
		return fn(r), sc
	}
	return baseConvention{name, runefunc}
}
