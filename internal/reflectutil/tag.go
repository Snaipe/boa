// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package reflectutil

import (
	"reflect"
	"strings"
	"unicode"
)

type Tag struct {
	Key     string
	Value   string
	Options []string
}

func LookupTag(tag reflect.StructTag, key string, options bool) (Tag, bool) {
	s := string(tag)
	for {
		i := strings.IndexFunc(s, func(r rune) bool {
			return !unicode.IsSpace(r)
		})
		if i == -1 {
			break
		}
		end := strings.IndexFunc(s, func(r rune) bool {
			return !unicode.In(r, unicode.L, unicode.Nd)
		})
		if end == -1 {
			end = len(s)
		}
		name := s[i:end]
		i = end
		if i >= len(s) {
			if s[i:] == key {
				return Tag{Key: s[i:]}, true
			}
			break
		}
		switch s[i] {
		case ':':
			i++
		case ' ':
			//continue
			return Tag{}, false
		default:
			return Tag{}, false
		}
		if s[i] != '"' {
			return Tag{}, false
		}
		s = s[i+1:]

		end = strings.IndexByte(s, '"')
		if end == -1 {
			return Tag{}, false
		}

		var opts []string
		value := s[:end]
		if options {
			split := strings.Split(value, ",")
			value = split[0]
			opts = split[1:]
		}
		if name == key {
			return Tag{Key: name, Value: value, Options: opts}, true
		}
		s = s[end+1:]
	}
	return Tag{}, false
}
