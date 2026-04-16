// Copyright 2026 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax_test

import (
	"io"
	"strings"
	"testing"

	"snai.pe/boa/syntax"
)

// reDigits matches one or more ASCII digits.
var reDigits = syntax.MustCompileRegexp("digits", `[0-9]+`)

// reDecimal matches a simple decimal float: digits, optional dot+digits, optional e+digits.
var reDecimal = syntax.MustCompileRegexp("decimal", `[0-9]+(\.[0-9]*)?([eE][0-9]+)?`)

func readAll(rs io.RuneScanner) (string, error) {
	var sb strings.Builder
	for {
		r, _, err := rs.ReadRune()
		if err == io.EOF {
			return sb.String(), nil
		}
		if err != nil {
			return sb.String(), err
		}
		sb.WriteRune(r)
	}
}

func TestRegexpScanner_Basic(t *testing.T) {
	r := strings.NewReader("123abc")
	s := reDigits.RuneScanner(r)

	got, err := readAll(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "123" {
		t.Errorf("got %q, want %q", got, "123")
	}

	// Verify "abc" is still in the underlying scanner.
	rest, _ := io.ReadAll(r)
	if string(rest) != "abc" {
		t.Errorf("remaining %q, want %q", rest, "abc")
	}
}

func TestRegexpScanner_NoMatch(t *testing.T) {
	r := strings.NewReader("abc")
	s := reDigits.RuneScanner(r)

	got, err := readAll(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}

	// All of "abc" is still in the underlying scanner.
	rest, _ := io.ReadAll(r)
	if string(rest) != "abc" {
		t.Errorf("remaining %q, want %q", rest, "abc")
	}
}

func TestRegexpScanner_DecimalFloat(t *testing.T) {
	tests := []struct {
		input string
		want  string
		rest  string
	}{
		{"123", "123", ""},
		{"1.5", "1.5", ""},
		{"1.", "1.", ""},
		{"1e2", "1e2", ""},
		{"1.5e3suffix", "1.5e3", "suffix"},
		// "1e2.3": after consuming "1e2" the NFA cannot advance with '.'.
		{"1e2.3", "1e2", ".3"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			r := strings.NewReader(tc.input)
			s := reDecimal.RuneScanner(r)

			got, err := readAll(s)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("consumed %q, want %q", got, tc.want)
			}
			rest, _ := io.ReadAll(r)
			if string(rest) != tc.rest {
				t.Errorf("remaining %q, want %q", rest, tc.rest)
			}
		})
	}
}

func TestRegexpScanner_UnreadRune(t *testing.T) {
	r := strings.NewReader("123")
	s := reDigits.RuneScanner(r)

	// Read one rune.
	r1, _, err := s.ReadRune()
	if err != nil {
		t.Fatalf("ReadRune: %v", err)
	}
	if r1 != '1' {
		t.Fatalf("got %q, want '1'", r1)
	}

	// Unread it.
	if err := s.UnreadRune(); err != nil {
		t.Fatalf("UnreadRune: %v", err)
	}

	// Read again – should get '1' again.
	r2, _, err := s.ReadRune()
	if err != nil {
		t.Fatalf("second ReadRune: %v", err)
	}
	if r2 != '1' {
		t.Fatalf("after unread got %q, want '1'", r2)
	}

	// Read the rest.
	got, err := readAll(s)
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if got != "23" {
		t.Errorf("got %q, want %q", got, "23")
	}
}

func TestRegexpScanner_EmptyInput(t *testing.T) {
	r := strings.NewReader("")
	s := reDigits.RuneScanner(r)

	_, _, err := s.ReadRune()
	if err != io.EOF {
		t.Errorf("got error %v, want io.EOF", err)
	}
}
