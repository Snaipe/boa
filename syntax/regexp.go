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
	"unicode/utf8"
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

// nfaState is a reusable NFA execution engine over a regexp/syntax.Prog.
type nfaState struct {
	prog *syntax.Prog
	cur  []uint32
	next []uint32
}

// step advances the NFA by consuming rune r. captures and last may be nil.
func (ns *nfaState) step(r rune, pos int, captures []int, last *int) bool {
	for i := 0; i < len(ns.cur); i++ {
		insn := ns.prog.Inst[ns.cur[i]]
		switch insn.Op {
		case syntax.InstAlt:
			ns.cur = append(ns.cur, insn.Out, insn.Arg)
		case syntax.InstAltMatch:
			if insn.MatchRune(r) {
				ns.next = append(ns.next, insn.Out, insn.Arg)
			}
		case syntax.InstEmptyWidth:
			ns.cur = append(ns.cur, insn.Out)
		case syntax.InstMatch:
			if last != nil {
				*last = pos
				if captures != nil {
					captures[1] = pos
				}
			}
		case syntax.InstFail:
		case syntax.InstRune, syntax.InstRune1:
			if insn.MatchRune(r) {
				ns.next = append(ns.next, insn.Out)
			}
		case syntax.InstCapture, syntax.InstNop:
			if captures != nil {
				captures[insn.Arg] = pos
			}
			ns.cur = append(ns.cur, insn.Out)
		case syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
			ns.next = append(ns.next, insn.Out)
		}
	}
	ns.cur, ns.next = ns.next, ns.cur[:0]
	return len(ns.cur) > 0
}

// expand runs epsilon transitions without consuming a rune (used at EOF).
func (ns *nfaState) expand(pos int, captures []int, last *int) {
	for i := 0; i < len(ns.cur); i++ {
		insn := ns.prog.Inst[ns.cur[i]]
		switch insn.Op {
		case syntax.InstAlt:
			ns.cur = append(ns.cur, insn.Out, insn.Arg)
		case syntax.InstEmptyWidth:
			ns.cur = append(ns.cur, insn.Out)
		case syntax.InstMatch:
			if last != nil {
				*last = pos
				if captures != nil {
					captures[1] = pos
				}
			}
		case syntax.InstCapture, syntax.InstNop:
			if captures != nil {
				captures[insn.Arg] = pos
			}
			ns.cur = append(ns.cur, insn.Out)
		}
	}
}

// RegexpScanner wraps an io.RuneScanner, gating reads through a greedy NFA.
// When the next rune would kill all threads, it is unread and io.EOF is returned.
type RegexpScanner struct {
	r   io.RuneScanner
	nfa nfaState

	prev    []uint32 // nfa.cur snapshot for UnreadRune
	hasPrev bool
}

// RuneScanner returns a RegexpScanner that reads from r.
func (re *Regexp) RuneScanner(r io.RuneScanner) *RegexpScanner {
	return &RegexpScanner{
		r: r,
		nfa: nfaState{
			prog: re.prog,
			cur:  []uint32{uint32(re.prog.Start)},
		},
	}
}

func (s *RegexpScanner) ReadRune() (rune, int, error) {
	if len(s.nfa.cur) == 0 {
		return 0, 0, io.EOF
	}

	r, w, err := s.r.ReadRune()
	if err != nil {
		return 0, 0, err
	}

	s.prev = append(s.prev[:0], s.nfa.cur...)

	if !s.nfa.step(r, 0, nil, nil) {
		s.r.UnreadRune() //nolint:errcheck
		s.nfa.cur, s.prev = s.prev, s.nfa.cur
		return 0, 0, io.EOF
	}

	s.hasPrev = true
	return r, w, nil
}

func (s *RegexpScanner) UnreadRune() error {
	if !s.hasPrev {
		return fmt.Errorf("regexp scanner: no rune to unread")
	}
	if err := s.r.UnreadRune(); err != nil {
		return err
	}
	s.nfa.cur, s.prev = s.prev, s.nfa.cur
	s.hasPrev = false
	return nil
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
		last = -1
		pos  int
		r    rune
		w    int
		ns   nfaState
	)

	out.WriteString(prefix)

	captures := make([]int, re.prog.NumCap)

	ns.prog = re.prog
	ns.cur = append(ns.cur, uint32(re.prog.Start))

	for len(ns.cur) != 0 {
		r, w, err = l.ReadRune()
		if err != nil {
			ns.expand(pos, captures, &last)
			break
		}
		out.WriteRune(r)
		ns.step(r, pos, captures, &last)
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

// StepFunc is one state in a character-by-character recognition machine.
// It consumes one rune and returns (next state, is-accepting).
// A nil next means the machine is dead.
type StepFunc func(rune) (StepFunc, bool)

// RegexpMachine runs a compiled Regexp as a StepFunc-compatible NFA.
type RegexpMachine struct {
	ns       nfaState
	peek     nfaState // epsilon-expand scratch for eager accept detection
	pos      int
	last     int   // byte offset of last accepted position (-1 = none)
	captures []int // [lo, hi) byte offset pairs
}

// NewMachine returns a fresh RegexpMachine ready to step through input.
func (re *Regexp) NewMachine() *RegexpMachine {
	// Pre-allocate state buffers to the program size. Each NFA state is an
	// instruction index, so len(prog.Inst) is a safe upper bound on the
	// live-state set at any point. This avoids append-growth allocations
	// inside Step for typical inputs.
	n := len(re.prog.Inst)
	caps := make([]int, re.prog.NumCap)
	for i := range caps {
		caps[i] = -1
	}
	m := &RegexpMachine{
		ns: nfaState{
			prog: re.prog,
			cur:  make([]uint32, 0, n),
			next: make([]uint32, 0, n),
		},
		peek: nfaState{
			prog: re.prog,
			cur:  make([]uint32, 0, n),
			next: make([]uint32, 0, n),
		},
		last:     -1,
		captures: caps,
	}
	m.ns.cur = append(m.ns.cur, uint32(re.prog.Start))
	// Eagerly detect acceptance of the empty string.
	m.peek.cur = append(m.peek.cur[:0], m.ns.cur...)
	m.peek.expand(0, m.captures, &m.last)
	return m
}

// Reset clears the machine state for reuse, keeping allocated buffers.
func (m *RegexpMachine) Reset() {
	m.ns.cur = m.ns.cur[:0]
	m.ns.cur = append(m.ns.cur, uint32(m.ns.prog.Start))
	m.ns.next = m.ns.next[:0]
	m.peek.cur = m.peek.cur[:0]
	m.peek.next = m.peek.next[:0]
	m.pos = 0
	m.last = -1
	for i := range m.captures {
		m.captures[i] = -1
	}
	// Eagerly detect acceptance of the empty string.
	m.peek.cur = append(m.peek.cur[:0], m.ns.cur...)
	m.peek.expand(0, m.captures, &m.last)
}

// FullMatch reports whether the entire input fed so far is a match.
func (m *RegexpMachine) FullMatch() bool {
	return m.last >= 0 && m.last == m.pos
}

// Step advances the machine by one rune. A nil next means the machine is dead.
func (m *RegexpMachine) Step(r rune) (StepFunc, bool) {
	w := utf8.RuneLen(r)
	if w < 1 {
		w = 1
	}
	prevLast := m.last
	alive := m.ns.step(r, m.pos, m.captures, &m.last)
	m.pos += w
	if alive {
		// Expand on a copy so we detect acceptance eagerly without
		// corrupting ns.cur for the next consuming step.
		m.peek.cur = append(m.peek.cur[:0], m.ns.cur...)
		m.peek.expand(m.pos, m.captures, &m.last)
	}
	if !alive {
		return nil, m.last != prevLast
	}
	return m.Step, m.last != prevLast
}

// Captures returns the captured substrings from text.
func (m *RegexpMachine) Captures(text string) []string {
	groups := make([]string, len(m.captures)/2)
	for i := range groups {
		lo, hi := m.captures[2*i], m.captures[2*i+1]
		if lo >= 0 && hi >= 0 && lo <= hi && hi <= len(text) {
			groups[i] = text[lo:hi]
		}
	}
	return groups
}
