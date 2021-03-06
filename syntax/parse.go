// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax

import (
	"fmt"
	"io"
	"strings"
)

type Marshaler interface {
	MarshalNode(*Node) error
	MarshalNodePost(*Node) error
}

type NodeType string

func (typ NodeType) String() string {
	return string(typ)
}

const (
	NodeDocument NodeType = "document"
	NodeMap      NodeType = "map"
	NodeList     NodeType = "list"
	NodeKeyPath  NodeType = "keypath"
	NodeString   NodeType = "string"
	NodeNumber   NodeType = "number"
	NodeBool     NodeType = "bool"
	NodeNil      NodeType = "nil"
	NodeDateTime NodeType = "datetime"
)

// Node represents a node in a parse tree.
type Node struct {
	// The type of node
	Type NodeType

	// The interpreted value of the node
	Value interface{}

	// The starting position of the node in the file (ignoring comments and whitespace)
	Position Cursor

	// The immediate sibling of the node (may be nil if no sibling)
	Sibling *Node

	// The first child of the node (may be nil if no children)
	Child *Node

	// Tokens contains the tokens that are part of this node element
	Tokens []Token

	// Suffix contains the tokens that are part of this node,
	// but appear after its children
	Suffix []Token
}

func (node *Node) text(out io.Writer, prefix string, start bool) {
	if start {
		fmt.Fprintf(out, "%s{\n", prefix)
	}
	fmt.Fprintf(out, "%s  %v = %v", prefix, node.Type, node.Value)

	if len(node.Tokens) > 0 {
		for _, tokens := range [][]Token{node.Tokens, node.Suffix} {
			fmt.Fprintf(out, "\n%s    [", prefix)
			for i, tok := range tokens {
				fmt.Fprintf(out, "{%v %q %d:%d}", tok.Type, tok.Raw, tok.Start.Line, tok.Start.Column)
				if i == len(tokens)-1 {
					io.WriteString(out, "]")
				} else {
					io.WriteString(out, ", ")
				}
			}
		}
	}
	if node.Child != nil {
		io.WriteString(out, ":\n")
		node.Child.text(out, prefix+"  ", true)
	}
	if node.Sibling != nil {
		fmt.Fprintf(out, ",\n")
		node.Sibling.text(out, prefix, false)
	} else {
		fmt.Fprintf(out, "\n%s}", prefix)
	}
}

func (node *Node) String() string {
	var out strings.Builder
	node.text(&out, "", true)
	return out.String()
}

func (node *Node) Marshal(marshaler Marshaler) error {
	for ; node != nil; node = node.Sibling {
		if err := marshaler.MarshalNode(node); err != nil {
			return err
		}
		if err := node.Child.Marshal(marshaler); err != nil {
			return err
		}
		if err := marshaler.MarshalNodePost(node); err != nil {
			return err
		}
	}
	return nil
}

func (node *Node) Trim(discard ...TokenType) Node {
	dup := *node
	dup.Suffix = nil
	if len(dup.Tokens) > 0 {
		lo, hi := 0, len(dup.Tokens)-1
		for dup.Tokens[lo].IsAny(discard...) {
			lo++
		}
		for dup.Tokens[hi].IsAny(discard...) {
			hi--
		}
		dup.Tokens = dup.Tokens[lo : hi+1]
	}
	dup.Sibling = nil
	return dup
}

type Parser interface {
	Parse() (*Node, error)
}
