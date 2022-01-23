// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax

import (
	"fmt"
	"io"
	"regexp/syntax"
	"strings"
)

// Accept is a more consistently-behaving regexp matcher. It uses regexp/syntax
// under the covers, and guarantees that no runes are read unnecessarily.
// It's not particularly fast, and does not optimize at all the regexp bytecode,
// but it is correct and conservative in its rune reading.
type Regexp struct {
	orig string
	name string
	prog *syntax.Prog
}

func CompileRegexp(name, s string) (*Regexp, error) {
	re, err := syntax.Parse(s, syntax.ClassNL|syntax.DotNL|syntax.OneLine|syntax.PerlX)
	if err != nil {
		return nil, err
	}
	prog, err := syntax.Compile(re.Simplify())
	if err != nil {
		return nil, err
	}
	return &Regexp{orig: s, name: name, prog: prog}, nil
}

func MustCompileRegexp(name, s string) *Regexp {
	re, err := CompileRegexp(name, s)
	if err != nil {
		panic(err)
	}
	return re
}

func (re *Regexp) String() string {
	if re.name != "" {
		return re.name
	}
	return re.orig
}

func (re *Regexp) GoString() string {
	return "`" + re.orig + "`"
}

// Accept accepts and returns the longest run of characters coming out of the
// lexer stream that matches the regular expression, or returns an error if it
// doesn't.
func (re *Regexp) Accept(l *Lexer) ([]string, error) {

	prefix, complete := re.prog.Prefix()
	if _, err := l.AcceptString(prefix); err != nil {
		return nil, err
	}
	if complete {
		return []string{prefix}, nil
	}

	var (
		out  strings.Builder
		err  error
		last int
		pos  int
		cur  []uint32
		next []uint32
		r    rune
		w    int
	)

	out.WriteString(prefix)

	captures := make([]int, re.prog.NumCap)

	cur = append(cur, uint32(re.prog.Start))
	last = -1
	for len(cur) != 0 {
		r, w, err = l.ReadRune()
		if err != nil {
			// Finish execution of any non-advancing instructions before
			// breaking out.
			for i := 0; i < len(cur); i++ {
				insn := re.prog.Inst[cur[i]]
				switch insn.Op {
				case syntax.InstAlt:
					cur = append(cur, insn.Out)
					cur = append(cur, insn.Arg)
				case syntax.InstEmptyWidth:
					cur = append(cur, insn.Out)
				case syntax.InstMatch:
					last = pos
					captures[1] = last
				case syntax.InstCapture, syntax.InstNop:
					captures[insn.Arg] = pos
					cur = append(cur, insn.Out)
				}
			}
			break
		}
		out.WriteRune(r)

		for i := 0; i < len(cur); i++ {
			insn := re.prog.Inst[cur[i]]
			switch insn.Op {
			case syntax.InstAlt:
				cur = append(cur, insn.Out)
				cur = append(cur, insn.Arg)
			case syntax.InstAltMatch:
				if insn.MatchRune(r) {
					next = append(next, insn.Out)
					next = append(next, insn.Arg)
				}
			case syntax.InstEmptyWidth:
				cur = append(cur, insn.Out)
			case syntax.InstMatch:
				last = pos
				captures[1] = last
			case syntax.InstFail:
			case syntax.InstRune, syntax.InstRune1:
				if insn.MatchRune(r) {
					next = append(next, insn.Out)
				}
			case syntax.InstCapture, syntax.InstNop:
				captures[insn.Arg] = pos
				cur = append(cur, insn.Out)
			case syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
				next = append(next, insn.Out)
			}
		}
		cur, next = next, cur[:0]
		pos += w
	}

	s := out.String()
	if err == nil {
		pos -= w
		s = s[:pos]
		l.UnreadRune()
	}

	if err == io.EOF {
		if last == -1 {
			err = io.ErrUnexpectedEOF
		} else {
			err = nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("character sequence %q does not match %v: %w", s, re, err)
	}
	if last != pos {
		return nil, fmt.Errorf("character sequence %q does not match %v", s, re)
	}
	results := make([]string, re.prog.NumCap/2)
	for i := range results {
		results[i] = s[len(prefix)+captures[2*i] : len(prefix)+captures[2*i+1]]
	}
	return results, nil
}
