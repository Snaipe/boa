// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax

import "testing"

func re(pattern string) *Regexp { return MustCompileRegexp("", pattern) }

func TestIsRegexpSubset(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		// Trivially equal languages.
		{`^null$`, `^null$`, true},
		{`^.*$`, `^.*$`, true},

		// Specific types are subsets of the catch-all.
		{`^null$`, `^.*$`, true},
		{`^(?:true|false)$`, `^.*$`, true},
		{`^(?:null|Null|NULL|~|)$`, `^.*$`, true},
		{`^<<$`, `^.*$`, true},
		{`^-?(?:0|[1-9][0-9]*)$`, `^.*$`, true},

		// Catch-all is not a subset of specific types.
		{`^.*$`, `^null$`, false},
		{`^.*$`, `^(?:true|false)$`, false},
		{`^.*$`, `^-?(?:0|[1-9][0-9]*)$`, false},

		// Specific types are not subsets of each other.
		{`^null$`, `^(?:true|false)$`, false},
		{`^(?:true|false)$`, `^null$`, false},
		{`^<<$`, `^(?:true|false)$`, false},

		// JSON int is a subset of JSON float (every integer is a valid float).
		{`^-?(?:0|[1-9][0-9]*)$`, `^-?(?:0|[1-9][0-9]*)(?:\.[0-9]*)?(?:[eE][-+]?[0-9]+)?$`, true},
		// JSON float is not a subset of JSON int ("1.5" is not an integer).
		{`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]*)?(?:[eE][-+]?[0-9]+)?$`, `^-?(?:0|[1-9][0-9]*)$`, false},

		// YAML1_1 binary/octal ints are not subsets of YAML1_1 floats.
		{`^[-+]?0b[0-1]+$`, `^(?:\.nan|[-+]?(?:\.inf|\.Inf|\.INF)|[-+]?(?:\.[0-9]+|[0-9]+(?:\.[0-9]*)?)(?:[eE][-+]?[0-9]+)?)$`, false},

		// Multiline-anchored catch-all behaves the same as the non-multiline form:
		// at the start/end of a single string, begin/end-line conditions hold too.
		{`^null$`, `(?m:^.*$)`, true},
		{`(?m:^.*$)`, `^null$`, false},
		{`(?m:^.*$)`, `^.*$`, true},
		{`^.*$`, `(?m:^.*$)`, true},
	}

	for _, tc := range cases {
		got := IsRegexpSubset(re(tc.a), re(tc.b))
		if got != tc.want {
			t.Errorf("IsRegexpSubset(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
