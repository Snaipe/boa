// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax

import (
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// IsRegexpSubset reports whether L(a) ⊆ L(b): every string matched by a is
// also matched by b.
func IsRegexpSubset(a, b *Regexp) bool {
	return !subsetWitness(a.prog, b.prog)
}

// MatchString reports whether s is matched by the regexp.
// For anchored patterns (^…$) this is a full-string match.
//
// Note: mid-string word-boundary assertions (\b, \B) and mid-string line
// anchors ((?m:^), (?m:$)) are not supported and will never be satisfied.
func (re *Regexp) MatchString(s string) bool {
	states := epsClose(re.prog, []uint32{uint32(re.prog.Start)}, syntax.EmptyBeginText|syntax.EmptyBeginLine)
	for _, r := range s {
		raw := nfaStep(re.prog, states, r)
		if len(raw) == 0 {
			return false
		}
		states = epsClose(re.prog, raw, 0)
	}
	states = epsClose(re.prog, states, syntax.EmptyEndText|syntax.EmptyEndLine)
	return isAccepting(re.prog, states)
}

// epsClose computes the epsilon closure of states under the specified
// empty-width conditions. Only InstEmptyWidth transitions whose required
// conditions are all present in flags are followed.
func epsClose(prog *syntax.Prog, states []uint32, flags syntax.EmptyOp) []uint32 {
	seen := make([]bool, len(prog.Inst))
	queue := make([]uint32, 0, len(states)+4)
	for _, s := range states {
		if !seen[s] {
			seen[s] = true
			queue = append(queue, s)
		}
	}
	for i := 0; i < len(queue); i++ {
		inst := prog.Inst[queue[i]]
		var nexts [2]uint32
		var n int
		switch inst.Op {
		case syntax.InstAlt, syntax.InstAltMatch:
			nexts[0], nexts[1] = inst.Out, uint32(inst.Arg)
			n = 2
		case syntax.InstCapture, syntax.InstNop:
			nexts[0] = inst.Out
			n = 1
		case syntax.InstEmptyWidth:
			// Follow this epsilon only if all required conditions are satisfied.
			if syntax.EmptyOp(inst.Arg)&^flags == 0 {
				nexts[0] = inst.Out
				n = 1
			}
		}
		for _, s := range nexts[:n] {
			if !seen[s] {
				seen[s] = true
				queue = append(queue, s)
			}
		}
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i] < queue[j] })
	return queue
}

// nfaStep returns the set of NFA states reachable from states by consuming r.
func nfaStep(prog *syntax.Prog, states []uint32, r rune) []uint32 {
	var result []uint32
	for _, s := range states {
		inst := prog.Inst[s]
		switch inst.Op {
		case syntax.InstRune:
			if runeInRanges(inst.Rune, syntax.Flags(inst.Arg), r) {
				result = append(result, inst.Out)
			}
		case syntax.InstRune1:
			if runeMatchesSingle(inst.Rune[0], syntax.Flags(inst.Arg), r) {
				result = append(result, inst.Out)
			}
		case syntax.InstRuneAny:
			result = append(result, inst.Out)
		case syntax.InstRuneAnyNotNL:
			if r != '\n' {
				result = append(result, inst.Out)
			}
		}
	}
	return result
}

func runeInRanges(ranges []rune, flags syntax.Flags, r rune) bool {
	for i := 0; i+1 < len(ranges); i += 2 {
		if ranges[i] <= r && r <= ranges[i+1] {
			return true
		}
	}
	if flags&syntax.FoldCase != 0 {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			for i := 0; i+1 < len(ranges); i += 2 {
				if ranges[i] <= f && f <= ranges[i+1] {
					return true
				}
			}
		}
	}
	return false
}

func runeMatchesSingle(single rune, flags syntax.Flags, r rune) bool {
	if r == single {
		return true
	}
	if flags&syntax.FoldCase != 0 {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f == single {
				return true
			}
		}
	}
	return false
}

func isAccepting(prog *syntax.Prog, states []uint32) bool {
	for _, s := range states {
		if prog.Inst[s].Op == syntax.InstMatch {
			return true
		}
	}
	return false
}

func statesKey(states []uint32) string {
	if len(states) == 0 {
		return ""
	}
	var b strings.Builder
	for i, s := range states {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatUint(uint64(s), 10))
	}
	return b.String()
}

func collectBreakpoints(prog *syntax.Prog) []rune {
	bpSet := map[rune]struct{}{0: {}, unicode.MaxRune + 1: {}}
	for _, inst := range prog.Inst {
		switch inst.Op {
		case syntax.InstRune:
			for i := 0; i+1 < len(inst.Rune); i += 2 {
				bpSet[inst.Rune[i]] = struct{}{}
				bpSet[inst.Rune[i+1]+1] = struct{}{}
			}
		case syntax.InstRune1:
			if len(inst.Rune) > 0 {
				bpSet[inst.Rune[0]] = struct{}{}
				bpSet[inst.Rune[0]+1] = struct{}{}
			}
		case syntax.InstRuneAnyNotNL:
			bpSet['\n'] = struct{}{}
			bpSet['\n'+1] = struct{}{}
		}
	}
	result := make([]rune, 0, len(bpSet))
	for r := range bpSet {
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// mergeBreakpoints merges two sorted breakpoint slices into one sorted slice
// with duplicates removed.
func mergeBreakpoints(a, b []rune) []rune {
	result := make([]rune, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			result = append(result, a[i])
			i++
		case a[i] > b[j]:
			result = append(result, b[j])
			j++
		default:
			result = append(result, a[i])
			i++
			j++
		}
	}
	result = append(result, a[i:]...)
	result = append(result, b[j:]...)
	return result
}

// subsetWitness returns true if there exists a string accepted by progA but
// not progB (i.e. L(progA) ⊄ L(progB)). Uses product-DFA BFS.
func subsetWitness(progA, progB *syntax.Prog) bool {
	bp := mergeBreakpoints(collectBreakpoints(progA), collectBreakpoints(progB))
	initA := epsClose(progA, []uint32{uint32(progA.Start)}, syntax.EmptyBeginText|syntax.EmptyBeginLine)
	initB := epsClose(progB, []uint32{uint32(progB.Start)}, syntax.EmptyBeginText|syntax.EmptyBeginLine)

	type pair struct {
		key  string
		a, b []uint32
	}
	newPair := func(a, b []uint32) pair {
		return pair{statesKey(a) + "|" + statesKey(b), a, b}
	}

	visited := map[string]bool{}
	enqueue := func(queue []pair, a, b []uint32) []pair {
		p := newPair(a, b)
		if !visited[p.key] {
			visited[p.key] = true
			queue = append(queue, p)
		}
		return queue
	}

	queue := enqueue(nil, initA, initB)
	for head := 0; head < len(queue); head++ {
		cur := queue[head]

		// Check if end-of-string here: A accepts but B doesn't → witness found.
		endA := epsClose(progA, cur.a, syntax.EmptyEndText|syntax.EmptyEndLine)
		endB := epsClose(progB, cur.b, syntax.EmptyEndText|syntax.EmptyEndLine)
		if isAccepting(progA, endA) && !isAccepting(progB, endB) {
			return true
		}

		for i := 0; i+1 < len(bp); i++ {
			r := bp[i]
			if r > unicode.MaxRune {
				break
			}
			nextARaw := nfaStep(progA, cur.a, r)
			if len(nextARaw) == 0 {
				continue // A is stuck on this char; no witness via this branch.
			}
			nextA := epsClose(progA, nextARaw, 0)
			nextB := epsClose(progB, nfaStep(progB, cur.b, r), 0)
			queue = enqueue(queue, nextA, nextB)
		}
	}
	return false
}
