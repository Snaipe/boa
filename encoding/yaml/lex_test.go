// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package yaml

import (
	"fmt"
	"testing"
)

const chompStrPlain = `         a
   b c           
    
     c


   d
`

const chompStrLit = `   a
   b	c           
    
     c


   d
`

func TestChompScalar(t *testing.T) {
	scalars := []struct {
		In     string
		Indent int
		Flags  int
		Out    string
	}{
		{
			In:     chompStrPlain,
			Flags:  mlfold | mlstrip,
			Indent: 3,
			Out:    "a b c\nc\n\nd",
		},
		{
			In:     chompStrLit,
			Flags:  mlliteral,
			Indent: 3,
			Out:    "a\nb\tc           \n \n  c\n\n\nd\n",
		},
		{
			In:     chompStrLit,
			Flags:  mlliteral | mlfold,
			Indent: 3,
			Out:    "a b\tc           \n \n  c\n\n\nd\n",
		},
		{
			In:    chompStrLit,
			Flags: mlliteral | 1,
			// Indent is the parent indent; explicit indicator 1 means content at
			// parent+1 = 0+1 = 1 (absolute column).
			Indent: 0,
			Out:    "  a\n  b\tc           \n   \n    c\n\n\n  d\n",
		},
		{
			In:     chompStrLit,
			Flags:  mlliteral | mlfold | 1,
			Indent: 0,
			Out:    "  a\n  b\tc           \n   \n    c\n\n\n  d\n",
		},
	}

	for i, scalar := range scalars {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			if actual := chompScalar(scalar.In, scalar.Flags, scalar.Indent); actual != scalar.Out {
				t.Fatalf("expected %q, got %q", scalar.Out, actual)
			}
		})
	}
}
