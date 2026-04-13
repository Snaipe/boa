// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package yaml

import (
	"fmt"
	"go/constant"
	"math"
	"math/big"
	"regexp"
	"strings"

	. "snai.pe/boa/syntax"
)

type resolver struct {
	regexp *regexp.Regexp
	tag    string
}

type Schema struct {
	name       string
	shorthands map[string]string
	resolvers  []resolver
	processors map[string]func(Node, TaggedValue) (Value, error)

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
			Option(
				RejectExplicitTags(),
				NoAnchorsOrAliases(),
				RejectFlowStyle(),
				EmptyScalarsAsStrings(),
				RejectDuplicateKeys(),
			)

	Failsafe = NewSchema("Failsafe").
			Tag("!!", "tag:yaml.org,2002:")

	JSON = NewSchema("JSON").
		Tag("!!", "tag:yaml.org,2002:").
		Type("tag:yaml.org,2002:null", `^null$`, processNil).
		Type("tag:yaml.org,2002:bool", `^(?:true|false)$`, processBool).
		Type("tag:yaml.org,2002:int", `^-?(?:0|[1-9][0-9]*)$`, processInt).
		Type("tag:yaml.org,2002:float", `^-?(?:0|[1-9][0-9]*)(?:\.[0-9]*)?(?:[eE][-+]?[0-9]+)?$`, processFloat)

	// YAML1_2 is the pure YAML 1.2 core schema. It does not include merge key
	// support ("<<"), which is not part of the YAML 1.2 specification.
	// Use YAML1_2WithMergeKey for the common extension that recognises "<<".
	YAML1_2 = NewSchema("YAML Core 1.2").
		Tag("!!", "tag:yaml.org,2002:").
		Type("tag:yaml.org,2002:null", `^(?:null|Null|NULL|~|)$`, processNil).
		Type("tag:yaml.org,2002:bool", `^(?:true|True|TRUE|false|False|FALSE)$`, processBool).
		Type("tag:yaml.org,2002:int", `^(?:[-+]?[0-9]+|0o[0-7]+|0x[0-9a-fA-F]+)$`, processInt).
		Type("tag:yaml.org,2002:float", `^(?:\.nan|\.NaN|\.NAN|[-+]?(?:\.inf|\.Inf|\.INF)|[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?)$`, processFloat).
		Type("tag:yaml.org,2002:str", `(?m:^.*$)`, processStr)

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
		Type("tag:yaml.org,2002:str", `(?m:^.*$)`, processStr)
)

func NewSchema(name string) *Schema {
	schema := Schema{
		name:       name,
		shorthands: make(map[string]string),
		processors: make(map[string]func(Node, TaggedValue) (Value, error)),
	}
	schema.Type("tag:yaml.org,2002:str", "", processStr)
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
		processors:          make(map[string]func(Node, TaggedValue) (Value, error), len(s.processors)),
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

// SchemaOption is a functional option that modifies a Schema.
// Use Option to apply one or more options to a schema.
type SchemaOption func(*Schema)

// Option applies opts to s and returns s, enabling builder-style chaining.
func (s *Schema) Option(opts ...SchemaOption) *Schema {
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// RejectExplicitTags returns a SchemaOption that causes the parser to reject
// any explicit YAML tag (e.g. !!int or !foo).
func RejectExplicitTags() SchemaOption {
	return func(s *Schema) { s.rejectExplicitTags = true }
}

// NoAnchorsOrAliases returns a SchemaOption that causes the parser to reject
// both anchor definitions (&name) and alias references (*name). Anchors and
// aliases always come in pairs, so there is no meaningful use-case for allowing
// one without the other.
func NoAnchorsOrAliases() SchemaOption {
	return func(s *Schema) { s.noAnchorsAliases = true }
}

// RejectFlowStyle returns a SchemaOption that causes the parser to reject
// flow-style mappings ({...}) and sequences ([...]).
func RejectFlowStyle() SchemaOption {
	return func(s *Schema) { s.rejectFlowStyle = true }
}

// EmptyScalarsAsStrings returns a SchemaOption that causes absent or empty
// scalar values to decode as empty strings instead of nil.
func EmptyScalarsAsStrings() SchemaOption {
	return func(s *Schema) { s.emptyScalarIsString = true }
}

// RejectDuplicateKeys returns a SchemaOption that causes the parser to reject
// mapping nodes that contain duplicate string keys.
func RejectDuplicateKeys() SchemaOption {
	return func(s *Schema) { s.rejectDuplicateKeys = true }
}

func (schema *Schema) Type(tag, re string, processor func(Node, TaggedValue) (Value, error)) *Schema {
	if re != "" {
		r := resolver{regexp.MustCompile(re), tag}
		// Insert before the trailing str catch-all (if any) so that callers can
		// safely append specific types after the schema is built without worrying
		// about resolver ordering.
		n := len(schema.resolvers)
		if n > 0 && schema.resolvers[n-1].tag == "tag:yaml.org,2002:str" {
			schema.resolvers = append(schema.resolvers, resolver{})
			schema.resolvers[n] = schema.resolvers[n-1]
			schema.resolvers[n-1] = r
		} else {
			schema.resolvers = append(schema.resolvers, r)
		}
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

// process resolves and processes a YAML intermediate value into a typed Value.
// base contains token annotations, val contains the YAML tagged value.
// children is used for sequences and mappings.
func (schema *Schema) process(base Node, val TaggedValue, children []yamlChild) (Value, error) {
	var err error
	if val.Tag == "" {
		if len(children) > 0 {
			if children[0].isMapping {
				val.Tag = "!<tag:yaml.org,2002:map>"
			} else {
				val.Tag = "!<tag:yaml.org,2002:seq>"
			}
		} else {
			val.Tag, err = schema.resolve(val.Scalar)
			if err != nil {
				return nil, err
			}
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
	return process(base, val)
}

// yamlChild represents a child element during YAML processing.
type yamlChild struct {
	isMapping bool
	key       Value
	value     Value
}

func (schema *Schema) processSeq(base Node, val TaggedValue) (Value, error) {
	return &List{Node: base}, nil
}

func (schema *Schema) processMap(base Node, val TaggedValue) (Value, error) {
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

func processMerge(base Node, val TaggedValue) (Value, error) {
	return &Merge{Node: base}, nil
}

func processNil(base Node, val TaggedValue) (Value, error) {
	return &Nil{Node: base}, nil
}

func processBool(base Node, val TaggedValue) (Value, error) {
	switch val.Scalar {
	case "y", "Y", "yes", "Yes", "YES", "true", "True", "TRUE", "on", "On", "ON":
		return &Bool{Node: base, Value: true}, nil
	case "n", "N", "no", "No", "NO", "false", "False", "FALSE", "off", "Off", "OFF":
		return &Bool{Node: base, Value: false}, nil
	default:
		return nil, fmt.Errorf("invalid boolean %v", val.Scalar)
	}
}

func processInt(base Node, val TaggedValue) (Value, error) {
	num := val.Scalar

	// Sexagesimal (base 60): e.g. "190:20:30" = 685230.
	// Only the YAML 1.1 schema regex matches this form; YAML 1.2 does not.
	if strings.Contains(num, ":") {
		neg := strings.HasPrefix(num, "-")
		s := strings.TrimLeft(num, "+-")
		result := new(big.Int)
		for _, seg := range strings.Split(s, ":") {
			component, ok := new(big.Int).SetString(seg, 10)
			if !ok {
				return nil, fmt.Errorf("parsing '%v': invalid sexagesimal integer component %q", num, seg)
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

	v, ok := new(big.Int).SetString(num, 0)
	if !ok {
		return nil, fmt.Errorf("parsing '%v': invalid integer", num)
	}
	constv := constant.Make(v)
	if constv.Kind() != constant.Int {
		panic("created int constant is not int")
	}
	return &Number{Node: base, Value: constv}, nil
}

func processFloat(base Node, val TaggedValue) (Value, error) {
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
			component, ok := new(big.Int).SetString(seg, 10)
			if !ok {
				return nil, fmt.Errorf("parsing '%v': invalid sexagesimal float component %q", num, seg)
			}
			intResult.Mul(intResult, big.NewInt(60))
			intResult.Add(intResult, component)
		}
		result := new(big.Float).SetPrec(prec).SetInt(intResult)

		if fracStr != "" {
			frac, _, err := big.ParseFloat(fracStr, 0, prec, big.ToNearestEven)
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

	v, _, err := big.ParseFloat(num, 0, prec, big.ToNearestEven)
	if err != nil {
		return nil, err
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

func processStr(base Node, val TaggedValue) (Value, error) {
	return &String{Node: base, Value: val.Scalar}, nil
}
