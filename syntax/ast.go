// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax

import "reflect"

// Value is the interface implemented by all AST value nodes.
// Any type that embeds Node and promotes its Base() method satisfies this interface.
type Value interface {
	// Base returns a pointer to the Node embedded in this node,
	// providing access to position and token annotation fields.
	Base() *Node
}

// Node contains fields common to all AST nodes for token-preserving
// round-trip marshaling.
type Node struct {
	// Position is the starting position of this node (ignoring leading
	// whitespace and comments).
	Position Cursor

	// Tokens contains the tokens that form the beginning of this node,
	// including any leading whitespace and comments.
	Tokens []Token

	// Suffix contains the tokens that follow this node's content, such as
	// trailing commas, whitespace, and comments.
	Suffix []Token
}

// Base implements Value.
func (n *Node) Base() *Node { return n }

// Trim returns a copy of this Node with its Suffix cleared and its Tokens
// trimmed of any leading/trailing tokens matching discard.
func (n Node) Trim(discard ...TokenType) Node {
	dup := n
	dup.Suffix = nil
	if len(dup.Tokens) > 0 {
		lo, hi := 0, len(dup.Tokens)-1
		for lo <= hi && dup.Tokens[lo].IsAny(discard...) {
			lo++
		}
		for hi >= lo && dup.Tokens[hi].IsAny(discard...) {
			hi--
		}
		if lo > hi {
			dup.Tokens = nil
		} else {
			dup.Tokens = dup.Tokens[lo : hi+1]
		}
	}
	return dup
}

// Document is the root node of a parsed document.
type Document struct {
	Node
	// Root is the top-level value of the document.
	Root Value
}

// Map is a mapping node (JSON5 object, TOML table, YAML mapping).
type Map struct {
	Node
	Entries []*MapEntry
}

// MapEntry is a key-value pair within a Map.
type MapEntry struct {
	// Key is the key of this entry. It is either a *String (for most formats)
	// or a KeyPather (for formats with dotted keys, e.g. TOML).
	Key   Value
	Value Value
}

// List is a sequence of values.
type List struct {
	Node
	Items []Value
}

// String is a string scalar value.
type String struct {
	Node
	Value string
}

// Number is a numeric scalar value.
// Value is either a go/constant.Value or a float64 (for Inf/NaN).
type Number struct {
	Node
	Value interface{}
}

// Bool is a boolean scalar value.
type Bool struct {
	Node
	Value bool
}

// Nil is a null/nil scalar value.
type Nil struct {
	Node
}

// KeyPather is implemented by map entry keys that represent a dotted key path
// (e.g. TOML's "a.b.c" keys). The concrete type is format-specific.
type KeyPather interface {
	Value
	KeyPathComponents() []interface{}
}

// TrimValue returns a shallow copy of v with its leading/trailing tokens
// matching discard removed, and its Suffix cleared.
// Children of compound nodes are not copied; they retain their original tokens.
func TrimValue(v Value, discard ...TokenType) Value {
	rv := reflect.ValueOf(v)
	dup := reflect.New(rv.Elem().Type())
	dup.Elem().Set(rv.Elem())
	out := dup.Interface().(Value)
	*out.Base() = out.Base().Trim(discard...)
	return out
}

// Marshaler is the interface for round-trip token-based marshaling of typed AST nodes.
type Marshaler interface {
	MarshalNode(Value) error
	MarshalNodePost(Value) error
}

// MarshalDocument traverses a Document's typed AST, calling m.MarshalNode before visiting
// each node's children and m.MarshalNodePost after.
func MarshalDocument(doc *Document, m Marshaler) error {
	if doc.Root == nil {
		return nil
	}
	return walkValue(doc.Root, m)
}

func walkValue(v Value, m Marshaler) error {
	if err := m.MarshalNode(v); err != nil {
		return err
	}
	switch node := v.(type) {
	case *Map:
		for _, entry := range node.Entries {
			if err := walkValue(entry.Key, m); err != nil {
				return err
			}
			if err := walkValue(entry.Value, m); err != nil {
				return err
			}
		}
	case *List:
		for _, item := range node.Items {
			if err := walkValue(item, m); err != nil {
				return err
			}
		}
	}
	return m.MarshalNodePost(v)
}

// Parser is the interface for format-specific parsers.
type Parser interface {
	Parse() (*Document, error)
}
