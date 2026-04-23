// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package yaml

import (
	"context"
	"fmt"
	"go/constant"
	"math"
	"math/big"
	"strings"
	"sync"

	. "snai.pe/boa/syntax"
)

type resolver struct {
	regexp *Regexp
	tag    string
}

type Schema struct {
	name       string
	shorthands map[string]string
	resolvers  []resolver
	processors map[string]func(context.Context, Node, TaggedValue) (Value, error)

	// resolversByFirstByte maps each ASCII first-byte value (0–127) to the
	// indices of resolvers whose regexp can possibly match a scalar starting
	// with that byte. Built lazily on first parse by buildFirstByteDispatch
	// and cached until the resolver set changes via firstByteOnce.
	firstByteOnce        sync.Once
	resolversByFirstByte [128][]int

	// Strict-mode restrictions applied via Option(). Each flag independently
	// rejects one YAML feature at parse time.
	rejectExplicitTags  bool // reject any !tag or !!tag annotation
	noAnchorsAliases    bool // reject both &name anchors and *name aliases
	rejectFlowStyle     bool // reject {...} flow mappings and [...] flow sequences
	emptyScalarIsString bool // decode empty/absent scalars as "" instead of nil
	rejectDuplicateKeys bool // reject mapping nodes with duplicate string keys
}

var (
	// DefaultSchema extends YAML1_2 with the widely-supported "<<"
	// merge key convention (tag:yaml.org,2002:merge). This is the default
	// schema used by the decoder.
	DefaultSchema = YAML1_2.Clone().
			Type("tag:yaml.org,2002:merge", `^<<$`, processMerge)

	// StrictYAML is the StrictYAML schema. It enforces five restrictions over
	// standard YAML: all untagged scalars are strings (no implicit typing, since
	// the only resolver matches any scalar), explicit tags are rejected, anchors
	// and aliases are rejected, flow-style collections are rejected, and
	// duplicate mapping keys are rejected.
	//
	// See https://hitchdev.com/strictyaml/ for the full specification.
	StrictYAML = NewSchema("StrictYAML").
			Tag("!!", "tag:yaml.org,2002:").
			Type("tag:yaml.org,2002:str", `^.*$`, processStr).
			RejectExplicitTags().
			NoAnchorsOrAliases().
			RejectFlowStyle().
			EmptyScalarsAsStrings().
			RejectDuplicateKeys()

	Failsafe = NewSchema("Failsafe").
			Tag("!!", "tag:yaml.org,2002:").
			Type("tag:yaml.org,2002:str", `^.*$`, processStr)

	JSON = NewSchema("JSON").
		Tag("!!", "tag:yaml.org,2002:").
		Type("tag:yaml.org,2002:null", `^null$`, processNil).
		Type("tag:yaml.org,2002:bool", `^(?:true|false)$`, processBool).
		Type("tag:yaml.org,2002:int", `^-?(?:0|[1-9][0-9]*)$`, processInt).
		Type("tag:yaml.org,2002:float", `^-?(?:0|[1-9][0-9]*)(?:\.[0-9]*)?(?:[eE][-+]?[0-9]+)?$`, processFloat).
		Type("tag:yaml.org,2002:str", `^.*$`, processStr)

	// YAML1_2 is the pure YAML 1.2 core schema. It does not include merge key
	// support ("<<"), which is not part of the YAML 1.2 specification.
	// Use DefaultSchema for the common extension that recognises "<<".
	YAML1_2 = NewSchema("YAML Core 1.2").
		Tag("!!", "tag:yaml.org,2002:").
		Type("tag:yaml.org,2002:null", `^(?:null|Null|NULL|~|)$`, processNil).
		Type("tag:yaml.org,2002:bool", `^(?:true|True|TRUE|false|False|FALSE)$`, processBool).
		Type("tag:yaml.org,2002:int", `^(?:[-+]?[0-9]+|0o[0-7]+|0x[0-9a-fA-F]+)$`, processInt).
		Type("tag:yaml.org,2002:float", `^(?:\.nan|\.NaN|\.NAN|[-+]?(?:\.inf|\.Inf|\.INF)|[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?)$`, processFloat).
		Type("tag:yaml.org,2002:str", `^.*$`, processStr)

	// YAML1_1 is the YAML 1.1 core schema. It extends YAML 1.2 with the broader
	// boolean forms accepted by YAML 1.1 (y/n, yes/no, on/off and their
	// capitalisation variants), the legacy octal integer prefix (0NNN), binary
	// integers (0bNNN), sexagesimal (base-60) integer and float literals
	// (e.g. 190:20:30 = 685230, 190:20:30.15 = 685230.15), and merge keys,
	// all from yaml.org/type/.
	YAML1_1 = NewSchema("YAML Core 1.1").
		Tag("!!", "tag:yaml.org,2002:").
		Type("tag:yaml.org,2002:null", `^(?:null|Null|NULL|~|)$`, processNil).
		Type("tag:yaml.org,2002:bool", `^(?:y|Y|yes|Yes|YES|true|True|TRUE|on|On|ON|n|N|no|No|NO|false|False|FALSE|off|Off|OFF)$`, processBool).
		Type("tag:yaml.org,2002:int", `^(?:[-+]?0b[0-1]+|[-+]?0[0-7]+|[-+]?(?:0|[1-9][0-9]*)|[-+]?0x[0-9a-fA-F]+|[-+]?[1-9][0-9]*(:[0-5]?[0-9])+)$`, processInt).
		Type("tag:yaml.org,2002:float", `^(?:\.nan|\.NaN|\.NAN|[-+]?(?:\.inf|\.Inf|\.INF)|[-+]?[0-9][0-9]*(:[0-5]?[0-9])+\.[0-9]*|[-+]?(?:\.[0-9]+|[0-9]+(?:\.[0-9]*)?)(?:[eE][-+]?[0-9]+)?)$`, processFloat).
		Type("tag:yaml.org,2002:merge", `^<<$`, processMerge).
		Type("tag:yaml.org,2002:str", `^.*$`, processStr)
)

func NewSchema(name string) *Schema {
	schema := Schema{
		name:       name,
		shorthands: make(map[string]string),
		processors: make(map[string]func(context.Context, Node, TaggedValue) (Value, error)),
	}
	schema.Type("tag:yaml.org,2002:seq", "", schema.processSeq)
	schema.Type("tag:yaml.org,2002:map", "", schema.processMap)
	return &schema
}

func (schema *Schema) parseShorthand(shortname string) (stem, param string) {
	if shortname[0] != '!' {
		panic("invalid shortname " + shortname + " (must be '!', '!!', or '!name!')")
	}

	stem = shortname
	param = ""
	if len(shortname) > 1 {
		end := strings.IndexRune(shortname[1:], '!')
		if end == -1 {
			// "!suffix" form: primary tag handle "!" with suffix
			return "!", shortname[1:]
		}
		stem, param = shortname[0:end+2], shortname[end+2:]
	}
	return stem, param
}

func (schema *Schema) lookupTag(tag string) string {
	if strings.HasPrefix(tag, "!<") && strings.HasSuffix(tag, ">") {
		return tag[2 : len(tag)-1]
	}
	stem, param := schema.parseShorthand(tag)
	prefix, ok := schema.shorthands[stem]
	if !ok {
		return ""
	}
	return prefix + param
}

func (schema *Schema) resolve(scalar string) (string, error) {
	for _, resolver := range schema.resolvers {
		if resolver.regexp.MatchString(scalar) {
			return resolver.tag, nil
		}
	}
	return "", fmt.Errorf("unresolvable scalar %q: scalar does not match any known tag in %s schema", scalar, schema.name)
}

// buildFirstByteDispatch populates resolversByFirstByte: for each ASCII byte b,
// the slice lists the resolver indices whose regexp can begin with b.
func (schema *Schema) buildFirstByteDispatch() {
	for b := 0; b < 128; b++ {
		schema.resolversByFirstByte[b] = schema.resolversByFirstByte[b][:0]
	}
	for i, r := range schema.resolvers {
		m := r.regexp.NewMachine()
		for b := 0; b < 128; b++ {
			m.Reset()
			next, _ := m.Step(rune(b))
			if next != nil || m.FullMatch() {
				schema.resolversByFirstByte[b] = append(schema.resolversByFirstByte[b], i)
			}
		}
	}
}

// newResolverMachines creates a set of RegexpMachine instances from the
// schema's resolvers, for lockstep NFA execution at lex time.
func (schema *Schema) newResolverMachines() ([]*RegexpMachine, []string) {
	schema.firstByteOnce.Do(schema.buildFirstByteDispatch)
	machines := make([]*RegexpMachine, len(schema.resolvers))
	tags := make([]string, len(schema.resolvers))
	for i, r := range schema.resolvers {
		machines[i] = r.regexp.NewMachine()
		tags[i] = r.tag
	}
	return machines, tags
}

func (schema *Schema) Tag(alias, tag string) *Schema {
	schema.parseShorthand(alias) // validates shorthand
	schema.shorthands[alias] = tag
	return schema
}

// Clone returns a deep copy of s. The copy can be modified independently
// (via Type, Tag, etc.) without affecting the original.
func (s *Schema) Clone() *Schema {
	dup := &Schema{
		name:                s.name,
		shorthands:          make(map[string]string, len(s.shorthands)),
		resolvers:           make([]resolver, len(s.resolvers)),
		processors:          make(map[string]func(context.Context, Node, TaggedValue) (Value, error), len(s.processors)),
		rejectExplicitTags:  s.rejectExplicitTags,
		noAnchorsAliases:    s.noAnchorsAliases,
		rejectFlowStyle:     s.rejectFlowStyle,
		emptyScalarIsString: s.emptyScalarIsString,
		rejectDuplicateKeys: s.rejectDuplicateKeys,
	}
	for k, v := range s.shorthands {
		dup.shorthands[k] = v
	}
	copy(dup.resolvers, s.resolvers)
	for k, v := range s.processors {
		dup.processors[k] = v
	}
	return dup
}

// RejectExplicitTags causes the parser to reject any explicit YAML tag
// (e.g. !!int or !foo). It returns s for builder-style chaining.
func (s *Schema) RejectExplicitTags() *Schema {
	s.rejectExplicitTags = true
	return s
}

// NoAnchorsOrAliases causes the parser to reject both anchor definitions
// (&name) and alias references (*name). Anchors and aliases always come in
// pairs, so there is no meaningful use-case for allowing one without the
// other. It returns s for builder-style chaining.
func (s *Schema) NoAnchorsOrAliases() *Schema {
	s.noAnchorsAliases = true
	return s
}

// RejectFlowStyle causes the parser to reject flow-style mappings ({...})
// and sequences ([...]). It returns s for builder-style chaining.
func (s *Schema) RejectFlowStyle() *Schema {
	s.rejectFlowStyle = true
	return s
}

// EmptyScalarsAsStrings causes absent or empty scalar values to decode as
// empty strings instead of nil. It returns s for builder-style chaining.
func (s *Schema) EmptyScalarsAsStrings() *Schema {
	s.emptyScalarIsString = true
	return s
}

// RejectDuplicateKeys causes the parser to reject mapping nodes that contain
// duplicate string keys. It returns s for builder-style chaining.
func (s *Schema) RejectDuplicateKeys() *Schema {
	s.rejectDuplicateKeys = true
	return s
}

// Type registers a type resolver for the given YAML tag. re is a regexp that
// matches the scalar values belonging to this type; when empty, no resolver is
// registered (useful for collection tags like !!seq and !!map whose type is
// inferred from structure, not content).
//
// Resolvers are kept in most-specific-first order: if the language of re is a
// strict subset of an already-registered resolver's language, the new resolver
// is inserted before that one. This means the caller does not need to worry
// about declaration order; a catch-all like `^.*$` (for !!str) will always
// sort to the end regardless of when Type is called.
func (schema *Schema) Type(tag, re string, processor func(context.Context, Node, TaggedValue) (Value, error)) *Schema {
	if re != "" {
		compiled := MustCompileRegexp("", re)
		r := resolver{compiled, tag}
		// Insert before the first existing resolver whose language is a superset
		// of the new one, keeping the list in most-specific-first order.
		pos := len(schema.resolvers)
		for i, existing := range schema.resolvers {
			if IsRegexpSubset(compiled, existing.regexp) {
				pos = i
				break
			}
		}
		schema.resolvers = append(schema.resolvers, resolver{})
		copy(schema.resolvers[pos+1:], schema.resolvers[pos:])
		schema.resolvers[pos] = r
		schema.firstByteOnce = sync.Once{}
	}
	schema.processors[tag] = processor
	return schema
}

func (schema *Schema) resolveTag(tag string) (string, error) {
	if strings.HasPrefix(tag, "!<") && strings.HasSuffix(tag, ">") {
		return tag[2 : len(tag)-1], nil
	}

	seen := map[string]struct{}{tag: {}}
	path := []string{tag}
	for strings.HasPrefix(tag, "!") {
		next := schema.lookupTag(tag)
		if next == "" {
			return "", fmt.Errorf("undefined tag %v", tag)
		}
		path = append(path, next)
		if _, ok := seen[next]; ok {
			return "", fmt.Errorf("loop in tag resolution: %v", strings.Join(path, " -> "))
		}
		tag = next
	}
	return tag, nil
}

// process resolves and processes a YAML scalar value into a typed Value.
// base contains token annotations; val carries the tag and scalar text.
func (schema *Schema) process(ctx context.Context, base Node, val TaggedValue) (Value, error) {
	var err error
	if val.Tag == "" {
		val.Tag, err = schema.resolve(val.Scalar)
		if err != nil {
			return nil, err
		}
	}
	val.Tag, err = schema.resolveTag(val.Tag)
	if err != nil {
		return nil, err
	}
	process, ok := schema.processors[val.Tag]
	if !ok {
		return nil, fmt.Errorf("undefined tag %v", val.Tag)
	}
	return process(ctx, base, val)
}

func (schema *Schema) processSeq(_ context.Context, base Node, val TaggedValue) (Value, error) {
	return &List{Node: base}, nil
}

func (schema *Schema) processMap(_ context.Context, base Node, val TaggedValue) (Value, error) {
	return &Map{Node: base}, nil
}

// Merge is a YAML merge key node produced when the plain scalar "<<" is
// resolved as tag:yaml.org,2002:merge. It implements syntax.MergeKey so
// that reflectutil can expand the associated mapping(s) into the parent map.
type Merge struct {
	Node
}

// IsMergeKey implements syntax.MergeKey.
func (*Merge) IsMergeKey() {}

func processMerge(_ context.Context, base Node, val TaggedValue) (Value, error) {
	return &Merge{Node: base}, nil
}

func processNil(_ context.Context, base Node, val TaggedValue) (Value, error) {
	return &Nil{Node: base}, nil
}

func processBool(_ context.Context, base Node, val TaggedValue) (Value, error) {
	switch val.Scalar {
	case "y", "Y", "yes", "Yes", "YES", "true", "True", "TRUE", "on", "On", "ON":
		return &Bool{Node: base, Value: true}, nil
	case "n", "N", "no", "No", "NO", "false", "False", "FALSE", "off", "Off", "OFF":
		return &Bool{Node: base, Value: false}, nil
	default:
		return nil, fmt.Errorf("invalid boolean %v", val.Scalar)
	}
}

func processInt(ctx context.Context, base Node, val TaggedValue) (Value, error) {
	num := val.Scalar

	// Sexagesimal (base 60): e.g. "190:20:30" = 685230.
	// Only the YAML 1.1 schema regex matches this form; YAML 1.2 does not.
	if strings.Contains(num, ":") {
		neg := strings.HasPrefix(num, "-")
		s := strings.TrimLeft(num, "+-")
		result := new(big.Int)
		for _, seg := range strings.Split(s, ":") {
			component, err := ParseBigInt(ctx, strings.NewReader(seg), 10)
			if err != nil {
				return nil, err
			}
			result.Mul(result, big.NewInt(60))
			result.Add(result, component)
		}
		if neg {
			result.Neg(result)
		}
		constv := constant.Make(result)
		if constv.Kind() != constant.Int {
			panic("created int constant is not int")
		}
		return &Number{Node: base, Value: constv}, nil
	}

	// For decimal integers (the common case), ParseNumber has an int64 fast path
	// that avoids big.Int allocation for values in the int64 range.
	// Prefixed integers (0x, 0o, 0b, or legacy 0NNN octal) are routed to
	// ParseBigInt which understands their syntax and signs correctly.
	stripped := strings.TrimLeft(num, "+-")
	var v interface{}
	var err error
	if len(stripped) > 1 && stripped[0] == '0' {
		// Any leading-zero form (prefixed or legacy octal): use ParseBigInt.
		r := strings.NewReader(num)
		bigv, berr := ParseBigInt(ctx, r, 0)
		if berr != nil {
			return nil, berr
		}
		if r.Len() > 0 {
			return nil, fmt.Errorf("invalid integer %q", num)
		}
		v = bigv
	} else {
		// Pure decimal (or plain "0"): ParseNumber has an int64 fast path.
		r := strings.NewReader(num)
		v, err = ParseNumber(ctx, r, 512, big.ToNearestEven)
		if err != nil {
			return nil, err
		}
		if r.Len() > 0 {
			return nil, fmt.Errorf("invalid integer %q", num)
		}
	}
	constv := constant.Make(v)
	if constv.Kind() != constant.Int {
		panic("created int constant is not int")
	}
	return &Number{Node: base, Value: constv}, nil
}

func processFloat(ctx context.Context, base Node, val TaggedValue) (Value, error) {
	const prec = 512 // matches current implementation of go/constant

	num := val.Scalar

	// big.ParseFloat does not recognize YAML special float literals (.inf, .nan).
	bare := strings.ToLower(strings.TrimLeft(num, "+-"))
	switch bare {
	case ".inf":
		sign := 1
		if strings.HasPrefix(num, "-") {
			sign = -1
		}
		return &Number{Node: base, Value: math.Inf(sign)}, nil
	case ".nan":
		return &Number{Node: base, Value: math.NaN()}, nil
	}

	// Sexagesimal float (YAML 1.1): e.g. "190:20:30.15" = 685230.15.
	// The integer part (before '.') is evaluated in base 60; the fractional
	// part (after '.') is always decimal. Pattern: [+-]?D+(:[0-5]?[0-9])+.D*
	if strings.Contains(num, ":") {
		neg := strings.HasPrefix(num, "-")
		s := strings.TrimLeft(num, "+-")

		// Split at the mandatory '.' to get integer and fractional parts.
		dot := strings.IndexByte(s, '.')
		intPart, fracStr := s, ""
		if dot >= 0 {
			intPart, fracStr = s[:dot], "0"+s[dot:]
		}

		// Accumulate sexagesimal integer part.
		intResult := new(big.Int)
		for _, seg := range strings.Split(intPart, ":") {
			component, err := ParseBigInt(ctx, strings.NewReader(seg), 10)
			if err != nil {
				return nil, err
			}
			intResult.Mul(intResult, big.NewInt(60))
			intResult.Add(intResult, component)
		}
		result := new(big.Float).SetPrec(prec).SetInt(intResult)

		if fracStr != "" {
			frac, err := ParseBigFloat(ctx, strings.NewReader(fracStr), prec, big.ToNearestEven)
			if err != nil {
				return nil, fmt.Errorf("parsing '%v': invalid sexagesimal float fraction: %w", num, err)
			}
			result.Add(result, frac)
		}
		if neg {
			result.Neg(result)
		}
		constv := constant.Make(result)
		if constv.Kind() != constant.Float {
			panic("created float constant is not float")
		}
		return &Number{Node: base, Value: constv}, nil
	}

	fr := strings.NewReader(num)
	v, err := ParseBigFloat(ctx, fr, prec, big.ToNearestEven)
	if err != nil {
		return nil, err
	}
	if fr.Len() > 0 {
		return nil, fmt.Errorf("invalid float %q", num)
	}
	var numVal interface{}
	if v.IsInf() {
		numVal = math.Inf(v.Sign())
	} else {
		constv := constant.Make(v)
		if constv.Kind() != constant.Float {
			panic("created float constant is not float")
		}
		numVal = constv
	}
	return &Number{Node: base, Value: numVal}, nil
}

func processStr(_ context.Context, base Node, val TaggedValue) (Value, error) {
	return &String{Node: base, Value: val.Scalar}, nil
}
