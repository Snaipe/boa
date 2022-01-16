// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax

import (
	"fmt"
	"strings"
)

var (
	ErrUnexpectedToken = UnexpectedTokenError{}
)

// Error represents a syntax error at a specific cursor position.
type Error struct {
	// The file path in which the error occured (optional)
	Filename string

	// The cursor position of the error
	Cursor

	// The concrete error
	Err error
}

func (e Error) Error() string {
	if e.Filename == "" {
		return fmt.Sprintf("at %d:%d: %v", e.Line, e.Column, e.Err)
	} else {
		return fmt.Sprintf("%s:%d:%d: %v", e.Filename, e.Line, e.Column, e.Err)
	}
}

func (e Error) Unwrap() error {
	return e.Err
}

type TokenTypeError struct {
	Type TokenType
	Err  error
}

func (e TokenTypeError) Error() string {
	return fmt.Sprintf("on token %v: %v", e.Type, e.Err)
}

func (e TokenTypeError) Unwrap() error {
	return e.Err
}

type UnexpectedTokenError []TokenType

func (e UnexpectedTokenError) Error() string {
	switch len(e) {
	case 0:
		return "unexpected token"
	case 1:
		return fmt.Sprintf("expected token %v", e[0])
	case 2:
		return fmt.Sprintf("expected token %v or %v", e[0], e[1])
	default:
		var out strings.Builder
		fmt.Fprintf(&out, "expected token %v", e[0])
		for i := 1; i < len(e)-1; i++ {
			fmt.Fprintf(&out, ", %v", e[i])
		}
		fmt.Fprintf(&out, ", or %v", e[len(e)-1])
		return out.String()
	}
}
