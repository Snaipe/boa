// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package yaml

import (
	"context"
	"fmt"
	"io"
	"strings"

	. "snai.pe/boa/syntax"
)

// Fake tokens for error reporting
const (
	tokenBlockEnd TokenType = "<yaml:block-end>"
)

// TaggedValue holds a YAML scalar or collection tag along with its string content.
type TaggedValue struct {
	Tag    string
	Scalar string
}

type parser struct {
	lexer      *Lexer
	lexerState *lexerState
	prev       []Token
	anchors    map[string]Value
	schema     *Schema
	ctx        context.Context
	flowDepth  int
	eof        bool // set when the stream is exhausted

	// dirEndLine is the source line of the most recent '---' marker.
	// Zero means no explicit document start was seen for the current document.
	// Used to detect block mapping keys on the same line as '---', which is
	// invalid per YAML 1.2 spec (block content cannot start on the --- line).
	dirEndLine int

	// flowMinIndent is the minimum column that content tokens in a flow
	// collection must appear on continuation lines (lines after the opening
	// bracket/brace). Set to parentIndent+2 when entering flow mode from block
	// context; reset to -1 when exiting flow mode. -1 means no minimum.
	flowMinIndent int

	// docTagShorthands holds the %TAG handle→prefix mappings active for the
	// current document. Reset to the two YAML defaults at each document start.
	docTagShorthands map[string]string
}

func newParser(ctx context.Context, in io.Reader, schema *Schema) Parser {
	lexer, lexerState, lexDone := newLexer(ctx, in)
	machines, tags, resolverDone := schema.newResolverMachines()
	lexerState.resolverMachines = machines
	lexerState.resolverTags = tags
	lexerState.resolversByFirstByte = schema.resolversByFirstByte[:]
	lexer.Done = func() {
		resolverDone()
		lexDone()
	}
	return &parser{
		lexer:            lexer,
		lexerState:       lexerState,
		anchors:          make(map[string]Value),
		schema:           schema,
		ctx:              ctx,
		flowMinIndent:    -1,
		docTagShorthands: make(map[string]string),
	}
}

func (p *parser) rawNext() Token {
	if len(p.prev) > 0 {
		last := len(p.prev) - 1
		tok := p.prev[last]
		p.prev = p.prev[:last]
		return tok
	}
	return p.lexer.Next()
}

func (p *parser) back(toks ...Token) {
	for i := len(toks) - 1; i >= 0; i-- {
		p.prev = append(p.prev, toks[i])
	}
}

func (p *parser) fail(tok Token, err error) {
	if tok.Type == TokenError {
		panic(tok.Value.(error))
	}
	if err == nil {
		err = fmt.Errorf("unexpected token %v", tok.Type)
	}
	panic(&Error{Cursor: tok.Start, Err: err})
}

// col returns the 0-indexed column position of a token.
func col(tok Token) int {
	if tok.Start.Column <= 0 {
		return 0
	}
	return tok.Start.Column - 1
}

// skip reads and discards tokens of the allowed types, collecting them if
// collect is non-nil, then returns the first non-allowed token.
func (p *parser) skip(collect *[]Token, allowed ...TokenType) Token {
	for {
		tok := p.rawNext()
		if tok.Type == TokenError {
			p.fail(tok, nil)
		}
		if !tok.IsAny(allowed...) {
			return tok
		}
		if collect != nil {
			*collect = append(*collect, tok)
		}
	}
}

// skipBlank skips whitespace, comments, newlines, and indent tokens.
// Returns the first non-blank token and whether a newline was crossed.
func (p *parser) skipBlank(collect *[]Token) (Token, bool) {
	crossed := false
	for {
		tok := p.rawNext()
		if tok.Type == TokenError {
			p.fail(tok, nil)
		}
		switch tok.Type {
		case TokenWhitespace, TokenComment:
			if collect != nil {
				*collect = append(*collect, tok)
			}
		case TokenNewline, TokenIndent:
			crossed = true
			if collect != nil {
				*collect = append(*collect, tok)
			}
		default:
			return tok, crossed
		}
	}
}

func (p *parser) Parse() (doc *Document, err error) {
	if p.eof {
		return nil, io.EOF
	}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*Error); ok {
				err = e
			} else if e, ok := r.(error); ok {
				err = e
			} else {
				panic(r)
			}
		}
	}()
	return p.Document(), nil
}

// Document parses a YAML document per the productions:
//
//	l-bare-document     ::= s-l+block-node(-1,block-in)
//	l-explicit-document ::= c-directives-end
//	                          ( l-bare-document
//	                          | (e-node s-l-comments) )
//	l-any-document      ::= l-directive-document
//	                      | l-explicit-document
//	                      | l-bare-document
//	l-directive-document ::= l-directive+ l-explicit-document
func (p *parser) Document() *Document {
	leading := make([]Token, 0, 4)
	tok, _ := p.skipBlank(&leading)

	// Reset per-document state: tag shorthands and anchor table.
	// YAML 1.2 spec §6.8.2: the two default handles are always defined.
	p.docTagShorthands = map[string]string{
		"!":  "!",
		"!!": "tag:yaml.org,2002:",
	}

	// Process YAML/TAG directives, validating format and detecting duplicates.
	sawDirectives := false
	sawYAML := false
	for tok.Type == TokenDirective {
		sawDirectives = true
		directive := tok.Value.(string)
		if isYAMLDirective(directive) {
			if sawYAML {
				p.fail(tok, fmt.Errorf("duplicate %%YAML directive in the same document"))
			}
			sawYAML = true
			if err := validateYAMLDirective(directive); err != nil {
				p.fail(tok, err)
			}
		} else if strings.HasPrefix(directive, "TAG ") {
			handle, prefix, err := parseTagDirective(directive)
			if err != nil {
				p.fail(tok, err)
			}
			p.docTagShorthands[handle] = prefix
		}
		leading = append(leading, tok)
		tok, _ = p.skipBlank(&leading)
	}

	// Per YAML 1.2 spec §9.2: l-directive-document requires l-directive+ l-explicit-document;
	// a document with directives MUST be followed by an explicit '---' marker.
	if sawDirectives && tok.Type != TokenDirectivesEnd {
		p.fail(tok, fmt.Errorf("YAML directive must be followed by '---' document-start marker"))
	}

	// Optional explicit document start marker (---)
	sawExplicit := false
	p.dirEndLine = 0 // clear from previous document
	if tok.Type == TokenDirectivesEnd {
		sawExplicit = true
		p.dirEndLine = tok.End.Line // record for block-key-on-same-line check
		leading = append(leading, tok)
		tok, _ = p.skipBlank(&leading)
	}

	switch tok.Type {
	case TokenEOF:
		p.eof = true
		return &Document{Root: &Nil{Node: Node{Tokens: leading}}}
	case TokenDocumentEnd:
		// "..." terminates the current (empty) document; consume it.
		leading = append(leading, tok)
		// Collect any trailing blank tokens (e.g. newline after '...') into leading
		// for round-trip encoding, then peek for EOF or next document.
		peekTok, _ := p.skipBlank(&leading)
		if peekTok.Type == TokenEOF {
			p.eof = true
		} else {
			p.back(peekTok)
		}
		if !sawExplicit {
			// A bare "..." not preceded by "---" is just a document-end marker
			// between documents, not an empty document itself. Attach its tokens
			// to the next document so they are preserved in round-trip encoding.
			nextDoc := p.Document()
			nextDoc.Root.Base().Tokens = append(leading, nextDoc.Root.Base().Tokens...)
			return nextDoc
		}
		return &Document{Root: &Nil{Node: Node{Tokens: leading}}}
	case TokenDirectivesEnd:
		// "---" belongs to the next document; put it back.
		p.back(tok)
		return &Document{Root: &Nil{Node: Node{Tokens: leading}}}
	}

	p.back(tok)
	// Use parentIndent=-2 so the modifier empty-node check (col <= parentIndent+1)
	// never fires at document level (any col >= 0 is valid content).
	root := p.Value(-2, false)

	// Consume optional document end / next-doc marker (but don't discard ---)
	suffix := &root.Base().Suffix
	// Remember how many tokens were already in the suffix before reading trailing
	// whitespace. Flow nodes (maps, sequences) store their closing delimiter in
	// Suffix, and we must not push those back when handing '---' to the next doc.
	preSuffixLen := len(*suffix)
	trailingTok, _ := p.skipBlank(suffix)
	switch trailingTok.Type {
	case TokenDocumentEnd:
		*suffix = append(*suffix, trailingTok)
		// Collect any trailing blank tokens (e.g. newline after '...') into suffix
		// for round-trip encoding, then peek for EOF or next document.
		peekTok, _ := p.skipBlank(suffix)
		if peekTok.Type == TokenEOF {
			p.eof = true
		} else {
			p.back(peekTok)
		}
	case TokenDirectivesEnd:
		// Belongs to the next document. Put back only the whitespace tokens that
		// were newly read by skipBlank (not the pre-existing suffix tokens, which
		// may include a flow document's closing '}' or ']') plus the "---" marker,
		// so the next Document() call picks them all up as its leading.
		newlyRead := (*suffix)[preSuffixLen:]
		p.back(append(newlyRead, trailingTok)...)
		*suffix = (*suffix)[:preSuffixLen]
	case TokenEOF:
		p.eof = true
	default:
		// Any other token after the document root is invalid trailing content.
		p.fail(trailingTok, fmt.Errorf("unexpected token %v after document content", trailingTok.Type))
	}

	// Prepend document-level leading tokens (directives, ---, leading whitespace)
	// into the root node's Tokens; MarshalDocument only walks Root, not doc.Node.
	root.Base().Tokens = append(leading, root.Base().Tokens...)
	return &Document{Root: root}
}

// value parses a YAML block or flow value per the productions:
//
//	s-l+block-node(n,c) ::= s-l+block-in-block(n,c) | s-l+flow-in-block(n)
//	s-l+flow-in-block(n) ::= s-separate(n+1,flow-out)
//	                          ns-flow-node(n+1,flow-out) s-b-comment
//	s-l+block-in-block(n,c) ::= s-l+block-scalar(n,c)
//	                           | s-l+block-collection(n,c)
//	s-l+block-collection(n,c) ::= ( s-separate(n+1,c)
//	                                c-ns-properties(n+1,c) )?
//	                              ( l+block-sequence(seq-spaces(n,c))
//	                              | l+block-mapping(n) )
//
// parentIndent is the minimum indent level (exclusive) that block content must
// exceed; -1 means accept any indent. flow is true inside a flow collection.
// inline is true when the value begins on the same line as its structural
// indicator ('-' or ':'), meaning the indent check is deferred to after the
// first newline crossing (InlineValue semantics).
func (p *parser) value(parentIndent int, flow bool, inline bool) Value {
	leading := make([]Token, 0, 4)
	tok, crossed := p.skipBlank(&leading)

	// Block end: if we crossed a newline and the indent is not deep enough, return null.
	if crossed && !flow && parentIndent >= 0 && col(tok) <= parentIndent {
		switch tok.Type {
		case TokenEOF, TokenDocumentEnd, TokenDirectivesEnd:
			// fall through to the switch below
		default:
			p.back(tok)
			return p.emptyScalar(leading)
		}
	}

	switch tok.Type {
	case TokenEOF, TokenDocumentEnd, TokenDirectivesEnd:
		p.back(tok)
		return p.emptyScalar(leading)
	case TokenComma, TokenRBrace, TokenRSquare:
		if flow {
			p.back(tok)
			return &Nil{Node: Node{Tokens: leading}}
		}
	}

	// entryCol tracks the column of the first token (anchor/tag or key) so that
	// block maps with leading anchors use the anchor's column as mapIndent.
	entryCol := col(tok)
	tok, mods := p.parseModifiers(tok, &leading)

	// In inline mode any duplicate anchor is an immediate error; in block mode
	// the check is deferred to the TokenAnchor/Tag case, where a second anchor
	// after a newline is valid iff it anchors a key inside a new block mapping.
	if inline && mods.hitSecondAnchor {
		p.fail(mods.hitSecondAnchorTok, fmt.Errorf("a node may only have one anchor"))
	}

	// If an anchor or tag was followed by a newline and the next token is not
	// at value-indent (it's at or before the parent indent), the node is empty.
	if mods.crossedNewline && (mods.anchor != "" || mods.tag != "") {
		isTerminator := tok.Type == TokenEOF || tok.Type == TokenDocumentEnd || tok.Type == TokenDirectivesEnd
		// In block mode, TokenDash is exempt: seq-spaces(n, block-out) = n-1
		// allows a block sequence value to start at the same column as the key.
		isDashStart := !inline && tok.Type == TokenDash
		// In block mode the threshold is parentIndent+1 (mapIndent); in inline
		// mode it is parentIndent (the dash column itself).
		threshold := parentIndent
		if !inline {
			threshold = parentIndent + 1
		}
		if isTerminator || (!isDashStart && !flow && col(tok) <= threshold) {
			if !isTerminator {
				// The newline was consumed inside the modifier loop. Restore it so
				// that parent parsers (e.g. BlockMapContinue) see crossed=true.
				p.back(Token{Type: TokenNewline}, tok)
			} else {
				p.back(tok)
			}
			val := p.resolveTaggedEmpty(mods.tag, leading)
			if mods.anchor != "" {
				p.anchors[mods.anchor] = val
			}
			return val
		}
		if !inline {
			// G9HC/H7J7: after crossing a newline, the anchor or tag must be more
			// indented than the parent mapping key (col > parentIndent+1 = mapIndent).
			// An anchor/tag at the mapping level is ambiguous and invalid.
			if !flow && !isTerminator {
				if mods.anchor != "" && col(mods.anchorTok) <= parentIndent+1 {
					p.fail(mods.anchorTok, fmt.Errorf("anchor is not indented enough: must be more than %d columns", parentIndent+1))
				}
				if mods.tag != "" && col(mods.tagTok) <= parentIndent+1 {
					p.fail(mods.tagTok, fmt.Errorf("tag is not indented enough: must be more than %d columns", parentIndent+1))
				}
			}
		}
		// Anchor/tag on a different line from content: the content's column
		// determines the block indent (not the anchor/tag's column).
		entryCol = col(tok)
	}

	// When a tag (or anchor) was collected but the next token is a flow
	// terminator (or EOF), the node value is empty — apply the tag schema.
	if mods.tag != "" || mods.anchor != "" {
		isTerminator := tok.Type == TokenEOF || tok.Type == TokenDocumentEnd || tok.Type == TokenDirectivesEnd
		isFlowEnd := flow && (tok.Type == TokenComma || tok.Type == TokenRBrace ||
			tok.Type == TokenRSquare || tok.Type == TokenColon)
		if isTerminator || isFlowEnd {
			p.back(tok)
			val := p.resolveTaggedEmpty(mods.tag, leading)
			if mods.anchor != "" {
				p.anchors[mods.anchor] = val
			}
			return val
		}
	}

	var val Value

	switch tok.Type {
	case TokenAlias:
		if p.schema.noAnchorsAliases {
			p.fail(tok, fmt.Errorf("aliases are not allowed in strict mode"))
		}
		name := tok.Value.(string)
		target, ok := p.anchors[name]
		if !ok {
			p.fail(tok, fmt.Errorf("undefined alias *%s", name))
		}
		alias := &Alias{
			Node:   Node{Tokens: append(leading, tok)},
			Name:   name,
			Target: target,
		}
		// In block (non-inline) context, an alias can be a mapping key: *alias: value.
		// When the alias is used as a key, any preceding anchor/tag applies to the
		// resulting mapping (not the alias itself), so no error is raised.
		if !inline && !flow {
			var between []Token
			colonTok := p.skip(&between, TokenWhitespace)
			if colonTok.Type == TokenColon {
				alias.Base().Suffix = append(append(alias.Base().Suffix, between...), colonTok)
				val = p.BlockMapContinue(entryCol, alias)
				break
			}
			p.back(append(between, colonTok)...)
		}
		// Standalone alias: per YAML 1.2 spec §7.1, cannot have anchor or tag.
		if mods.anchor != "" || mods.tag != "" {
			p.fail(tok, fmt.Errorf("alias node cannot have anchor or tag properties"))
		}
		val = alias

	case TokenLSquare:
		if p.schema.rejectFlowStyle {
			p.fail(tok, fmt.Errorf("flow sequences are not allowed in strict mode"))
		}
		// In inline mode parentIndent is the block indent (e.g. dashIndent), so
		// subtract 1 to give flowMinIndent = parentIndent+1 rather than +2.
		flowOffset := parentIndent
		if inline {
			flowOffset--
		}
		p.enterFlow(flowOffset)
		val = p.FlowSeq(tok, leading)
		p.exitFlow()
		val = p.tryAsBlockMapKey(flow, val, tok, entryCol)

	case TokenLBrace:
		if p.schema.rejectFlowStyle {
			p.fail(tok, fmt.Errorf("flow mappings are not allowed in strict mode"))
		}
		flowOffset := parentIndent
		if inline {
			flowOffset--
		}
		p.enterFlow(flowOffset)
		val = p.FlowMap(tok, leading)
		p.exitFlow()
		val = p.tryAsBlockMapKey(flow, val, tok, entryCol)

	case TokenDash:
		if flow {
			p.fail(tok, fmt.Errorf("block sequence not allowed in flow context"))
		}
		// In block mode, sequences (l+block-collection) must start on a new line;
		// they cannot appear on the same line as an anchor/tag or mapping key.
		// Exception: at the document root (parentIndent == -2) with no modifiers.
		// In inline mode this guard does not apply (the '-' already crossed a line).
		if !inline && !crossed && !mods.crossedNewline && (mods.anchor != "" || mods.tag != "" || parentIndent > -2) {
			p.fail(tok, fmt.Errorf("block sequence cannot appear on the same line as a mapping key or anchor"))
		}
		seqIndent := col(tok)
		p.back(tok)
		val = p.BlockSeq(seqIndent, leading)

	case TokenQuery:
		if flow {
			p.fail(tok, fmt.Errorf("explicit block key '?' not allowed in flow context"))
		}
		mapIndent := col(tok)
		p.back(tok)
		val = p.BlockMapExplicit(mapIndent, leading)

	case TokenScalar, TokenString:
		// Decide: block mapping or plain scalar?
		// In inline mode always try implicit-key detection; in block mode only
		// when we have crossed a newline or are at the document root — this
		// prevents treating a value-on-same-line as a nested mapping key.
		//
		// ZCZ6/ZL4Z: a block mapping value (parentIndent >= -1) on the same line
		// as the mapping colon (!crossed) cannot itself be an implicit mapping key.
		// Block mappings as values must start on a new line (l+block-collection).
		scalarIsMultiline := strings.ContainsAny(tok.Raw, "\r\n")
		if !flow && !scalarIsMultiline && (inline || crossed || mods.crossedNewline || parentIndent < -1) {
			var between []Token
			colonTok := p.skip(&between, TokenWhitespace)
			if colonTok.Type == TokenColon && p.peekIsKeyEnd() {
				// Block mapping keys are not allowed on the same line as '---'.
				// Per YAML 1.2 spec, block content requires a newline after '---';
				// only flow nodes may appear on the same line.
				if !inline && p.dirEndLine > 0 && tok.Start.Line == p.dirEndLine {
					p.fail(tok, fmt.Errorf("block mapping key not allowed on same line as '---' document-start marker"))
				}
				key := p.ResolveScalar(tok, leading, "")
				// Store whitespace-before-colon and the colon itself in key.Suffix
				// so the encoder can reconstruct them verbatim.
				key.Base().Suffix = append(between, colonTok)
				// Anchor on an implicit key refers to the KEY scalar only when
				// the anchor and key are on the same line (no newline crossed).
				// When a newline was crossed, the anchor is on the VALUE (the
				// whole map), handled by the post-switch anchor registration.
				if !inline && mods.anchor != "" && !mods.crossedNewline {
					p.anchors[mods.anchor] = key
					mods.anchor = ""
				}
				val = p.BlockMapContinue(entryCol, key)
				break
			}
			p.back(append(between, colonTok)...)
		}
		val = p.ResolveScalar(tok, leading, mods.tag)
		mods.tag = ""

	case TokenBlock:
		// Block scalar (| or >): lexer emits TokenBlock then TokenScalar with content.
		// Block scalars are always strings; use !!str unless an explicit tag was given.
		blockTag := mods.tag
		if blockTag == "" {
			blockTag = "!!str"
		}
		leading = append(leading, tok)
		scalarTok, _ := p.skipBlank(&leading)
		if scalarTok.Type == TokenScalar {
			val = p.ResolveScalar(scalarTok, leading, blockTag)
			mods.tag = ""
		} else {
			// Empty block scalar
			p.back(scalarTok)
			val = &String{Node: Node{Tokens: leading, Position: tok.Start}, Value: ""}
		}

	case TokenAnchor, TokenTag:
		// More than 2 properties: a modifier on the inner node. Recurse with
		// the inline flag flipped so the two modes alternate correctly.
		p.back(tok)
		val = p.value(parentIndent, flow, !inline)
		if !inline && mods.hitSecondAnchor {
			// When we arrive here because of a second anchor after a newline, the
			// outer anchor anchors a container (a block mapping) and the inner anchor
			// anchors a key or inner node. If the content is not a *Map, both anchors
			// alias the same non-Map node — that's two anchors on one node: invalid.
			if _, ok := val.(*Map); !ok {
				p.fail(mods.hitSecondAnchorTok, fmt.Errorf("a node may only have one anchor"))
			}
		}
		// Prepend accumulated leading tokens (anchor/tag/newline from outer node)
		// to the inner value's tokens so round-trip encoding preserves them.
		val.Base().Tokens = append(leading, val.Base().Tokens...)

	case TokenColon:
		if flow {
			// In flow context, ':' signals an empty implicit key (e.g. "{: val}",
			// "[: val]"). Return Nil and push the colon back so the caller handles it.
			p.back(tok)
			return p.resolveTaggedEmpty(mods.tag, leading)
		}
		// Block context: ':' with no preceding key → empty implicit key (e-node).
		if p.peekIsKeyEnd() {
			key := p.resolveTaggedEmpty(mods.tag, leading)
			mods.tag = ""
			key.Base().Suffix = []Token{tok}
			if !inline && mods.anchor != "" && !mods.crossedNewline {
				p.anchors[mods.anchor] = key
				mods.anchor = ""
			}
			val = p.BlockMapContinue(entryCol, key)
			break
		}
		p.fail(tok, fmt.Errorf("unexpected token ':' in value context"))
		panic("unreachable")

	default:
		p.fail(tok, fmt.Errorf("unexpected token %v in value context", tok.Type))
		panic("unreachable")
	}

	if mods.anchor != "" {
		p.anchors[mods.anchor] = val
	}

	return val
}

// Value parses a YAML block or flow value. parentIndent is the minimum indent
// level (exclusive) that block content must exceed; -1 means accept any indent.
// flow is true inside a flow collection.
func (p *parser) Value(parentIndent int, flow bool) Value {
	return p.value(parentIndent, flow, false)
}

// InlineValue parses a YAML value that begins on the current line. Unlike
// Value, it does not apply the block-indent check to the very first token;
// the check is deferred until a newline is actually crossed. This corresponds
// to the content portion of block-indented contexts where the value may begin
// on the same line as the structural indicator ('-' or ':'):
//
//	s-l+block-indented(n,c) ::= s-l+block-node(n,c) | (e-node s-b-comment)
func (p *parser) InlineValue(parentIndent int, flow bool) Value {
	return p.value(parentIndent, flow, true)
}

// BlockSeq parses a block sequence per the production:
//
//	l+block-sequence(n) ::= ( s-indent(n+m)
//	                          c-l-block-seq-entry(n+m) )+
//	                        /* For some fixed auto-detected m > 0 */
//
// seqIndent is the column of the '-' markers; leading contains tokens already
// consumed before the first entry.
func (p *parser) BlockSeq(seqIndent int, leading []Token) *List {
	lst := &List{Node: Node{Tokens: leading}}

	for {
		skipped := make([]Token, 0, 4)
		tok, _ := p.skipBlank(&skipped)

		switch tok.Type {
		case TokenEOF, TokenDocumentEnd, TokenDirectivesEnd:
			// Store inter-entry trailing whitespace in the list suffix for round-trip.
			lst.Node.Suffix = append(lst.Node.Suffix, skipped...)
			p.back(tok)
			return lst
		}

		if tok.Type != TokenDash || col(tok) != seqIndent {
			p.back(append(skipped, tok)...)
			return lst
		}

		// Parse the value of this sequence item; pass inter-entry whitespace so it
		// can be prepended to the item's Tokens for round-trip encoding.
		item := p.SeqItem(tok, seqIndent, skipped)
		lst.Items = append(lst.Items, item)
	}
}

// SeqItem parses one block sequence entry per the productions:
//
//	c-l-block-seq-entry(n) ::= "-" ( s-b-comment
//	                               | s-l+block-indented(n,block-in) )
//	s-l+block-indented(n,c) ::= s-indent(m)
//	                            ( ns-l-compact-sequence(n+1+m)
//	                            | ns-l-compact-mapping(n+1+m) )
//	                          | s-l+block-node(n,c)
//	                          | (e-node s-b-comment)
//
// dash is the already-consumed '-' token; dashIndent is its column position.
// preceding contains inter-entry blank tokens collected before the dash by BlockSeq.
func (p *parser) SeqItem(dash Token, dashIndent int, preceding []Token) Value {
	// Build the pre-item token sequence: [inter-entry whitespace..., dash, ws-after-dash...]
	preleading := append(preceding, dash)

	// Skip same-line whitespace only (not newlines) to peek at what follows.
	tok := p.skip(&preleading, TokenWhitespace)

	var item Value
	switch tok.Type {
	case TokenNewline, TokenComment, TokenEOF, TokenDocumentEnd, TokenDirectivesEnd:
		// Value is on the next line(s).
		p.back(tok)
		item = p.Value(dashIndent, false)
	default:
		// Value is on the same line as '-'.
		p.back(tok)
		item = p.InlineValue(dashIndent, false)
	}

	// Prepend [preceding..., dash, ws-after-dash...] to the item's Tokens so the
	// encoder can reconstruct them verbatim.
	item.Base().Tokens = append(preleading, item.Base().Tokens...)
	return item
}

// BlockMapContinue builds a block mapping given the first key (already parsed)
// and parses all subsequent entries at the same indent per the productions:
//
//	l+block-mapping(n) ::= ( s-indent(n+m)
//	                         ns-l-block-map-entry(n+m) )+
//	                       /* For some fixed auto-detected m > 0 */
//	ns-l-block-map-entry(n) ::= c-l-block-map-explicit-entry(n)
//	                          | ns-l-block-map-implicit-entry(n)
//	ns-l-block-map-implicit-entry(n) ::= ( ns-s-block-map-implicit-key
//	                                     | c-l-block-map-explicit-key(n) )
//	                                     s-l+block-map-implicit-val(n)
//	s-l+block-map-implicit-val(n) ::= ":" ( s-b-comment
//	                                      | s-l+block-node(n,block-out) )
//
// mapIndent is the column of the key tokens.
func (p *parser) BlockMapContinue(mapIndent int, firstKey Value) *Map {
	m := &Map{Node: Node{Position: firstKey.Base().Position}}

	// In strict mode, track seen string keys to detect duplicates.
	var seenKeys map[string]bool
	if p.schema.rejectDuplicateKeys {
		seenKeys = make(map[string]bool)
		if s, ok := firstKey.(*String); ok {
			seenKeys[s.Value] = true
		}
	}

	// Parse the value for the first key.
	// YAML spec seq-spaces(n, block-out) = n-1: a block sequence value of a mapping
	// entry may start at the same column as the key (parentIndent = mapIndent-1).
	firstVal := p.Value(mapIndent-1, false)
	m.Entries = append(m.Entries, &MapEntry{Key: firstKey, Value: firstVal})

	// Parse subsequent entries.
	for {
		skipped := make([]Token, 0, 4)
		tok, crossed := p.skipBlank(&skipped)

		switch tok.Type {
		case TokenEOF, TokenDocumentEnd, TokenDirectivesEnd:
			// Store trailing inter-entry whitespace in the map suffix for round-trip.
			m.Node.Suffix = append(m.Node.Suffix, skipped...)
			p.back(tok)
			return m
		}

		// A new entry must be at the same indent, on a new line.
		if !crossed || col(tok) != mapIndent {
			p.back(append(skipped, tok)...)
			return m
		}

		switch tok.Type {
		case TokenScalar, TokenString:
			// Implicit keys must be single-line per YAML 1.2 spec §6.3.2.
			if strings.ContainsAny(tok.Raw, "\r\n") {
				p.back(tok)
				return m
			}
			var between []Token
			colonTok := p.skip(&between, TokenWhitespace)
			if colonTok.Type != TokenColon || !p.peekIsKeyEnd() {
				// Not a key: end of map.
				p.back(tok, colonTok)
				return m
			}
			// skipped = inter-entry whitespace; between = ws between key and colon.
			key := p.ResolveScalar(tok, skipped, "")
			if seenKeys != nil {
				if s, ok := key.(*String); ok {
					if seenKeys[s.Value] {
						p.fail(tok, fmt.Errorf("duplicate key %q", s.Value))
					}
					seenKeys[s.Value] = true
				}
			}
			key.Base().Suffix = append(between, colonTok)
			val := p.Value(mapIndent-1, false)
			m.Entries = append(m.Entries, &MapEntry{Key: key, Value: val})

		case TokenQuery:
			// Explicit key within an existing block map.
			p.back(tok)
			subMap := p.BlockMapExplicit(mapIndent, skipped)
			// Merge entries into current map.
			m.Entries = append(m.Entries, subMap.(*Map).Entries...)

		case TokenAlias:
			// Alias as a mapping key (rare but valid).
			if p.schema.noAnchorsAliases {
				p.fail(tok, fmt.Errorf("aliases are not allowed in strict mode"))
			}
			name := tok.Value.(string)
			target, ok := p.anchors[name]
			if !ok {
				p.fail(tok, fmt.Errorf("undefined alias *%s", name))
			}
			alias := &Alias{
				Node:   Node{Tokens: append(skipped, tok)},
				Name:   name,
				Target: target,
			}
			var betweenColon []Token
			colonTok := p.skip(&betweenColon, TokenWhitespace)
			if colonTok.Type != TokenColon {
				p.back(tok, colonTok)
				return m
			}
			alias.Base().Suffix = append(betweenColon, colonTok)
			val := p.Value(mapIndent-1, false)
			m.Entries = append(m.Entries, &MapEntry{Key: alias, Value: val})

		case TokenAnchor, TokenTag:
			// Mapping key with anchor and/or tag before the key scalar.
			// Reuse parseModifiers so we don't duplicate its logic; push everything
			// back (inter-entry whitespace + modifiers + next token) when what
			// follows the modifiers is not a usable implicit key.
			tok, mods := p.parseModifiers(tok, &skipped)
			if tok.Type != TokenScalar && tok.Type != TokenString {
				p.back(append(skipped, tok)...)
				return m
			}
			var between []Token
			colonTok := p.skip(&between, TokenWhitespace)
			if colonTok.Type != TokenColon || !p.peekIsKeyEnd() {
				p.back(append(skipped, tok)...)
				p.back(append(between, colonTok)...)
				return m
			}
			key := p.ResolveScalar(tok, skipped, mods.tag)
			if seenKeys != nil {
				if s, ok := key.(*String); ok {
					if seenKeys[s.Value] {
						p.fail(tok, fmt.Errorf("duplicate key %q", s.Value))
					}
					seenKeys[s.Value] = true
				}
			}
			key.Base().Suffix = append(between, colonTok)
			if mods.anchor != "" {
				p.anchors[mods.anchor] = key
			}
			val := p.Value(mapIndent-1, false)
			m.Entries = append(m.Entries, &MapEntry{Key: key, Value: val})

		case TokenColon:
			// Empty implicit key in a subsequent block mapping entry (e.g. ": val").
			if !p.peekIsKeyEnd() {
				p.back(tok)
				return m
			}
			key := &Nil{Node: Node{Tokens: skipped}}
			key.Base().Suffix = []Token{tok}
			val := p.Value(mapIndent-1, false)
			m.Entries = append(m.Entries, &MapEntry{Key: key, Value: val})

		default:
			p.back(tok)
			return m
		}
	}
}

// BlockMapExplicit parses a block mapping that uses explicit '?' key notation
// per the productions:
//
//	c-l-block-map-explicit-entry(n) ::= c-l-block-map-explicit-key(n)
//	                                    ( l-block-map-explicit-value(n)
//	                                    | e-node )
//	c-l-block-map-explicit-key(n)   ::= "?" s-l+block-indented(n,block-out)
//	l-block-map-explicit-value(n)   ::= s-indent(n) ":"
//	                                    s-l+block-indented(n,block-out)
//
// mapIndent is the column of the '?' markers; leading contains tokens
// already consumed before the first entry.
func (p *parser) BlockMapExplicit(mapIndent int, leading []Token) Value {
	m := &Map{Node: Node{Tokens: leading}}

	for {
		skipped := make([]Token, 0, 4)
		tok, crossed := p.skipBlank(&skipped)

		switch tok.Type {
		case TokenEOF, TokenDocumentEnd, TokenDirectivesEnd:
			// Store trailing whitespace in map suffix for round-trip encoding.
			m.Node.Suffix = append(m.Node.Suffix, skipped...)
			p.back(tok)
			return m
		}

		if crossed && col(tok) != mapIndent {
			p.back(append(skipped, tok)...)
			return m
		}

		switch tok.Type {
		case TokenQuery:
			// Prepend [inter-entry whitespace..., ?] to the key's Tokens.
			queryTok := tok

			// Peek ahead to determine if key content is on the same line as '?'
			// or on the next line. YAML allows inline block sequences and mappings
			// after '?', which require parentIndent=-2 to disable the same-line
			// block collection guards in Node(). For content on the next line, use
			// the standard mapIndent enforcement.
			var keyPeekLeading []Token
			keyPeekTok, keyCrossed := p.skipBlank(&keyPeekLeading)
			p.back(append(keyPeekLeading, keyPeekTok)...)

			var key Value
			if keyCrossed {
				// Content starts on next line: use mapIndent-1 so the block-end check
				// (col(tok) <= parentIndent) does not fire when the content is at the
				// same column as '?'. Zero-indented sequences (mapIndent=0, col=0) need
				// parentIndent=-1 so -1 >= 0 is false and the check is skipped entirely.
				key = p.Value(mapIndent-1, false)
			} else {
				// Content starts on same line as '?': allow inline block sequences
				// (- item) and inline block mappings (key: val) by using parentIndent=-2.
				key = p.Value(-2, false)
			}
			key.Base().Tokens = append(append(skipped, queryTok), key.Base().Tokens...)

			// Optionally parse ': value'.
			// Collect the blank tokens so we can restore them (including any newline)
			// if no colon follows — the parent parser needs to see crossed=true.
			var val Value
			var peekSkipped []Token
			peekTok, peekCrossed := p.skipBlank(&peekSkipped)
			if peekTok.Type == TokenColon && (!peekCrossed || col(peekTok) == mapIndent) {
				// Store whitespace-before-colon and colon in key.Suffix for round-trip.
				key.Base().Suffix = append(append(key.Base().Suffix, peekSkipped...), peekTok)
				// Use NodeSameLine(-2) so that compact sequences and other block content
				// are allowed immediately after ':' on the same line (e.g. ': - item')
				// or as the value (e.g. ': key: val').
				val = p.InlineValue(-2, false)
			} else {
				p.back(append(peekSkipped, peekTok)...)
				val = &Nil{}
			}
			m.Entries = append(m.Entries, &MapEntry{Key: key, Value: val})

		case TokenScalar, TokenString:
			// Mixed implicit key in an explicit-key map.
			var between []Token
			colonTok := p.skip(&between, TokenWhitespace)
			if colonTok.Type != TokenColon {
				p.back(tok, colonTok)
				return m
			}
			key := p.ResolveScalar(tok, skipped, "")
			key.Base().Suffix = append(between, colonTok)
			val := p.Value(mapIndent-1, false)
			m.Entries = append(m.Entries, &MapEntry{Key: key, Value: val})

		case TokenColon:
			// Empty implicit key in a subsequent explicit-block mapping entry (": val").
			if !p.peekIsKeyEnd() {
				p.back(tok)
				return m
			}
			key := &Nil{Node: Node{Tokens: skipped}}
			key.Base().Suffix = []Token{tok}
			val := p.Value(mapIndent-1, false)
			m.Entries = append(m.Entries, &MapEntry{Key: key, Value: val})

		default:
			p.back(append(skipped, tok)...)
			return m
		}
	}
}

// FlowSeq parses a flow sequence per the productions:
//
//	c-flow-sequence(n,c) ::= "[" s-separate(n,c)?
//	                         ( ns-s-flow-seq-entries(n,in-flow(c))
//	                           s-separate(n,c)? )?
//	                         "]"
//	ns-s-flow-seq-entries(n,c) ::= ns-flow-seq-entry(n,c)
//	                               s-separate(n,c)?
//	                               ( "," s-separate(n,c)?
//	                                 ns-s-flow-seq-entries(n,c)? )?
//
// open is the already-consumed '[' token; leading contains tokens consumed
// before it.
func (p *parser) FlowSeq(open Token, leading []Token) *List {
	// Store '[' in the list's Tokens for round-trip encoding.
	lst := &List{Node: Node{Tokens: append(leading, open), Position: open.Start}}

	for {
		// Collect blank tokens before each item or ']' for round-trip encoding.
		before := make([]Token, 0, 4)
		tok, crossed := p.skipBlank(&before)
		p.checkFlowIndent(tok, crossed)

		if tok.Type == TokenRSquare {
			// Store trailing whitespace and ']' in lst.Suffix.
			lst.Node.Suffix = append(before, tok)
			break
		}
		if tok.Type == TokenEOF {
			p.fail(tok, fmt.Errorf("unexpected EOF in flow sequence"))
		}
		// Per YAML 1.2 spec, flow sequence entries must start with a value — a bare
		// ',' at the beginning of an entry (leading or consecutive comma) is invalid.
		if tok.Type == TokenComma {
			p.fail(tok, fmt.Errorf("unexpected ',' in flow sequence: missing value before ','"))
		}

		// A flow-seq item may be a flow-pair (implicit key: value).
		p.back(tok)
		item := p.FlowSeqItem()
		// Prepend inter-element whitespace to item's Tokens for round-trip.
		item.Base().Tokens = append(before, item.Base().Tokens...)
		lst.Items = append(lst.Items, item)

		// Collect blank tokens after the item (before ',' or ']').
		after := make([]Token, 0, 4)
		sep, crossed := p.skipBlank(&after)
		p.checkFlowIndent(sep, crossed)
		if sep.Type == TokenComma {
			// Comma goes in item.Suffix; whitespace after comma picked up next iteration.
			item.Base().Suffix = append(item.Base().Suffix, append(after, sep)...)
			continue
		}
		if sep.Type == TokenRSquare {
			// Trailing whitespace goes in item.Suffix; ']' goes in lst.Suffix.
			item.Base().Suffix = append(item.Base().Suffix, after...)
			lst.Node.Suffix = append(lst.Node.Suffix, sep)
			break
		}
		p.fail(sep, fmt.Errorf("expected ',' or ']' in flow sequence, got %v", sep.Type))
	}

	return lst
}

// FlowSeqItem parses one item from a flow sequence per the productions:
//
//	ns-flow-seq-entry(n,c) ::= ns-flow-pair(n,c) | ns-flow-node(n,c)
//	ns-flow-pair(n,c)      ::= ( "?" s-separate(n,c)
//	                             ns-flow-map-explicit-entry(n,c) )
//	                         | ns-flow-pair-entry(n,c)
//	ns-flow-pair-entry(n,c) ::= ns-flow-pair-yaml-key-entry(n,c)
//	                          | c-ns-flow-map-adjacent-value(n,c)
//	                          | c-ns-flow-map-json-key-entry(n,c)
func (p *parser) FlowSeqItem() Value {
	tok, _ := p.skipBlank(nil)

	switch tok.Type {
	case TokenQuery:
		// Explicit key in flow sequence: ? key : value
		// Prepend the '?' token to the key's Tokens for round-trip encoding.
		queryTok := tok
		key := p.Value(-1, true)
		key.Base().Tokens = append([]Token{queryTok}, key.Base().Tokens...)
		var beforeColon []Token
		colonTok, _ := p.skipBlank(&beforeColon)
		var val Value
		if colonTok.Type == TokenColon {
			// Append colon to existing Suffix (key may already have closing delimiters).
			key.Base().Suffix = append(append(key.Base().Suffix, beforeColon...), colonTok)
			val = p.Value(-1, true)
		} else {
			p.back(colonTok)
			val = &Nil{}
		}
		m := &Map{Node: Node{Position: tok.Start}}
		m.Entries = []*MapEntry{{Key: key, Value: val}}
		return m

	case TokenScalar, TokenString:
		// Check for implicit flow pair: scalar: value
		// For JSON (quoted-string) keys, the ':' can be adjacent (no space required).
		var between []Token
		colonTok := p.skip(&between, TokenWhitespace)
		if colonTok.Type == TokenColon {
			after := p.rawNext()
			isJSONKey := tok.Type == TokenString
			isKey := isJSONKey || after.Type == TokenEOF ||
				after.Type == TokenNewline ||
				after.Type == TokenWhitespace ||
				after.Type == TokenComma ||
				after.Type == TokenRSquare ||
				after.Type == TokenRBrace
			p.back(after)
			if isKey {
				// Implicit flow keys must be on a single line (YAML 1.2 spec §7.3.2).
				// The lexer folds continuation lines into the scalar's Raw, so a
				// newline in Raw means the colon appeared after a line break.
				if strings.ContainsAny(tok.Raw, "\r\n") {
					p.fail(colonTok, fmt.Errorf("implicit flow mapping key must be on a single line"))
				}
				key := p.ResolveScalar(tok, nil, "")
				// Store whitespace-before-colon and colon in key.Suffix for round-trip.
				key.Base().Suffix = append(between, colonTok)
				val := p.Value(-1, true)
				m := &Map{Node: Node{Position: tok.Start}}
				m.Entries = []*MapEntry{{Key: key, Value: val}}
				return m
			}
		}
		// Not a flow pair: put the non-colon token back and parse as a plain value.
		// The whitespace in 'between' was consumed when checking for ':'; attach it
		// to the parsed node's Suffix so it is emitted verbatim during round-trip.
		p.back(tok, colonTok)
		item := p.Value(-1, true)
		item.Base().Suffix = append(between, item.Base().Suffix...)
		return item
	default:
		p.back(tok)
		key := p.Value(-1, true)
		// An anchor, tag, alias, or other complex node may be a flow pair key.
		// Implicit flow keys must be on a single line — if we crossed a newline
		// before the colon, this is a multi-line key (invalid per YAML 1.2 §7.3.2).
		var beforeColon []Token
		colonTok, crossedBeforeColon := p.skipBlank(&beforeColon)
		if colonTok.Type == TokenColon {
			if crossedBeforeColon {
				p.fail(colonTok, fmt.Errorf("implicit flow mapping key must be on a single line"))
			}
			key.Base().Suffix = append(append(key.Base().Suffix, beforeColon...), colonTok)
			val := p.Value(-1, true)
			m := &Map{Node: Node{Position: key.Base().Position}}
			m.Entries = []*MapEntry{{Key: key, Value: val}}
			return m
		}
		// Not a flow pair: attach the whitespace before the non-colon token to the
		// node's Suffix so it is not lost during round-trip encoding.
		p.back(colonTok)
		key.Base().Suffix = append(key.Base().Suffix, beforeColon...)
		return key
	}
}

// FlowMap parses a flow mapping per the productions:
//
//	c-flow-mapping(n,c)       ::= "{" s-separate(n,c)?
//	                              ( ns-s-flow-map-entries(n,in-flow(c))
//	                                s-separate(n,c)? )?
//	                              "}"
//	ns-s-flow-map-entries(n,c) ::= ns-flow-map-entry(n,c)
//	                               s-separate(n,c)?
//	                               ( "," s-separate(n,c)?
//	                                 ns-s-flow-map-entries(n,c)? )?
//	ns-flow-map-entry(n,c)    ::= ( "?" s-separate(n,c)
//	                                ns-flow-map-explicit-entry(n,c) )
//	                            | ns-flow-map-implicit-entry(n,c)
//
// open is the already-consumed '{' token; leading contains tokens consumed
// before it.
func (p *parser) FlowMap(open Token, leading []Token) *Map {
	// Store '{' in the map's Tokens for round-trip encoding.
	m := &Map{Node: Node{Tokens: append(leading, open), Position: open.Start}}

	for {
		// Collect blank tokens before each key or '}' for round-trip encoding.
		before := make([]Token, 0, 4)
		tok, crossed := p.skipBlank(&before)
		p.checkFlowIndent(tok, crossed)

		if tok.Type == TokenRBrace {
			// Store trailing whitespace and '}' in map.Suffix.
			m.Node.Suffix = append(before, tok)
			break
		}
		if tok.Type == TokenEOF {
			p.fail(tok, fmt.Errorf("unexpected EOF in flow mapping"))
		}

		// Parse key.
		// Track the type of the first key token so we can distinguish JSON-style
		// (quoted-string) from YAML-style (plain scalar) implicit keys.
		keyStartType := tok.Type
		var key Value
		if tok.Type == TokenQuery {
			// Explicit key in flow mapping: prepend [before..., ?] to key's Tokens.
			key = p.Value(-1, true)
			key.Base().Tokens = append(append(before, tok), key.Base().Tokens...)
		} else {
			p.back(tok)
			key = p.Value(-1, true)
			// Prepend inter-entry whitespace to key's Tokens.
			key.Base().Tokens = append(before, key.Base().Tokens...)
		}

		// Parse ': value' (optional for explicit keys).
		// For YAML-style (plain scalar) implicit keys the colon must appear on the
		// same line (YAML 1.2 spec §7.4.2 ns-s-implicit-yaml-key). JSON-style
		// (quoted-string) keys may have the colon on the next line.
		var val Value
		var beforeColon []Token
		colonTok, crossedBeforeColon := p.skipBlank(&beforeColon)
		p.checkFlowIndent(colonTok, crossedBeforeColon)
		if colonTok.Type == TokenColon {
			isYAMLKey := keyStartType != TokenQuery && keyStartType != TokenString
			if isYAMLKey && crossedBeforeColon {
				p.fail(colonTok, fmt.Errorf("implicit flow mapping key must be on a single line"))
			}
			// Store whitespace-before-colon and colon in key.Suffix for round-trip.
			key.Base().Suffix = append(append(key.Base().Suffix, beforeColon...), colonTok)
			val = p.Value(-1, true)
		} else {
			// No colon: key has no value; attach whitespace to key's Suffix so it
			// is not lost during round-trip encoding (e.g. the space in "{a }").
			key.Base().Suffix = append(key.Base().Suffix, beforeColon...)
			p.back(colonTok)
			val = &Nil{}
		}

		m.Entries = append(m.Entries, &MapEntry{Key: key, Value: val})

		// Collect blank tokens after value (before ',' or '}').
		afterVal := make([]Token, 0, 4)
		sep, crossed := p.skipBlank(&afterVal)
		p.checkFlowIndent(sep, crossed)
		if sep.Type == TokenComma {
			// Comma goes in val.Suffix; whitespace after comma picked up next iteration.
			val.Base().Suffix = append(val.Base().Suffix, append(afterVal, sep)...)
			continue
		}
		if sep.Type == TokenRBrace {
			// Trailing whitespace in val.Suffix; '}' in map.Suffix.
			val.Base().Suffix = append(val.Base().Suffix, afterVal...)
			m.Node.Suffix = append(m.Node.Suffix, sep)
			break
		}
		p.fail(sep, fmt.Errorf("expected ',' or '}' in flow mapping, got %v", sep.Type))
	}

	return m
}

// ResolveScalar resolves a lexed scalar token to a typed AST node by applying
// the schema's tag resolver per the YAML 1.2 core schema rules:
//
//	ns-plain(n,c) | c-single-quoted(n,c) | c-double-quoted(n,c)
//	  → TaggedValue{Tag, Scalar}
//	  → schema.process
//	  → *Nil | *Bool | *Number | *String
//
// The tag parameter is the explicit tag (e.g. "!!str") if one was specified,
// or empty to trigger automatic tag resolution.
func (p *parser) ResolveScalar(tok Token, leading []Token, tag string) Value {
	base := Node{Tokens: append(leading, tok), Position: tok.Start}
	var scalar string
	var resolvedTag string
	switch v := tok.Value.(type) {
	case resolvedScalar:
		scalar = v.Value
		resolvedTag = v.Tag
	case string:
		scalar = v
	default:
		scalar = tok.Raw
	}

	// Quoted scalars (double- or single-quoted) are always strings per the YAML spec.
	// Force !!str unless the caller already supplied an explicit override tag.
	if tag == "" && tok.Type == TokenString {
		tag = "!!str"
	} else if tag == "" && resolvedTag != "" {
		tag = resolvedTag
	}
	tv := TaggedValue{Tag: tag, Scalar: scalar}
	val, err := p.schema.process(p.ctx, base, tv)
	if err != nil {
		return &String{Node: base, Value: scalar}
	}
	return val
}

// resolveTaggedEmpty returns a typed empty node for a tag with no content.
// For !!str this is an empty string; for other tags the schema decides.
// When tag is empty, returns a plain Nil.
// emptyScalar returns the Value for an absent/empty scalar with no tag.
// In strict mode (schema.emptyScalarIsString) this is an empty String;
// otherwise it is the conventional YAML Nil.
func (p *parser) emptyScalar(tokens []Token) Value {
	base := Node{Tokens: tokens}
	if p.schema.emptyScalarIsString {
		return &String{Node: base, Value: ""}
	}
	return &Nil{Node: base}
}

func (p *parser) resolveTaggedEmpty(tag string, leading []Token) Value {
	if tag == "" {
		return p.emptyScalar(leading)
	}
	base := Node{Tokens: leading}
	tv := TaggedValue{Tag: tag, Scalar: ""}
	val, err := p.schema.process(p.ctx, base, tv)
	if err != nil {
		return &String{Node: base, Value: ""}
	}
	return val
}

// isYAMLDirective reports whether the directive value (after the leading '%')
// starts with the "YAML" keyword followed by a space/tab or end of string.
func isYAMLDirective(directive string) bool {
	return strings.HasPrefix(directive, "YAML") &&
		(len(directive) == 4 || directive[4] == ' ' || directive[4] == '\t')
}

// validateYAMLDirective checks that a %YAML directive has a valid version number
// (e.g. "YAML 1.2") with no trailing garbage.
func validateYAMLDirective(directive string) error {
	// directive is e.g. "YAML 1.2" — strip leading "YAML" keyword
	rest := strings.TrimLeft(directive[4:], " \t")
	if rest == "" {
		return fmt.Errorf("%%YAML directive missing version number")
	}

	// Parse major.minor manually
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i == 0 {
		return fmt.Errorf("%%YAML directive has invalid version: %q", rest)
	}
	if i >= len(rest) || rest[i] != '.' {
		return fmt.Errorf("%%YAML directive has invalid version: %q", rest)
	}
	i++ // skip '.'
	j := i
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i == j {
		return fmt.Errorf("%%YAML directive has invalid version: %q", rest)
	}

	// Nothing else allowed after the version except optional whitespace and an
	// optional inline comment starting with '#' (per spec production l-directive
	// which allows s-b-comment after the directive keyword).
	tail := rest[i:]
	if tail == "" {
		return nil
	}
	if tail[0] != ' ' && tail[0] != '\t' {
		return fmt.Errorf("%%YAML directive has extra content after version: %q", tail)
	}
	trimmed := strings.TrimLeft(tail, " \t")
	if trimmed != "" && trimmed[0] != '#' {
		return fmt.Errorf("%%YAML directive has extra content after version: %q", trimmed)
	}
	return nil
}

// parseTagDirective parses a %TAG directive value (without the leading '%').
// Returns the handle (e.g. "!prefix!") and prefix URI, or an error.
func parseTagDirective(directive string) (handle, prefix string, err error) {
	// directive is e.g. "TAG !prefix! tag:example.com,2011:"
	fields := strings.Fields(directive)
	if len(fields) != 3 || fields[0] != "TAG" {
		return "", "", fmt.Errorf("invalid %%TAG directive format: %q", directive)
	}
	handle = fields[1]
	prefix = fields[2]
	if handle != "!" && (!strings.HasPrefix(handle, "!") || !strings.HasSuffix(handle, "!") || len(handle) < 2) {
		return "", "", fmt.Errorf("invalid %%TAG handle %q: must start and end with '!'", handle)
	}
	return handle, prefix, nil
}

// validateTagHandle checks that a tag's handle (if any) is defined in the
// current document's %TAG directives. Fails if an unknown named handle is used.
func (p *parser) validateTagHandle(tok Token, tag string) {
	// Verbatim tags (!<uri>) are always valid.
	if strings.HasPrefix(tag, "!<") {
		return
	}
	handle, _ := p.schema.parseShorthand(tag)
	if _, ok := p.docTagShorthands[handle]; !ok {
		p.fail(tok, fmt.Errorf("tag handle %q is not defined in this document", handle))
	}
}

// enterFlow increments the flow nesting depth and enables flow-mode lexing.
// When entering the outermost flow collection (depth 1), it sets flowMinIndent
// so that continuation lines are indented by at least parentIndent+2 columns.
func (p *parser) enterFlow(parentIndent int) {
	p.flowDepth++
	p.lexerState.FlowMode = true
	if p.flowDepth == 1 {
		p.flowMinIndent = parentIndent + 2
	}
}

// exitFlow decrements the flow nesting depth. When the last flow collection
// is closed (depth reaches 0), it disables flow-mode lexing and resets flowMinIndent.
func (p *parser) exitFlow() {
	p.flowDepth--
	if p.flowDepth == 0 {
		p.lexerState.FlowMode = false
		p.flowMinIndent = -1
	}
}

// checkFlowIndent validates that a content token on a continuation line within
// a flow collection is indented by at least flowMinIndent columns. Closing
// brackets/braces and commas are exempt (they terminate structure, not content).
func (p *parser) checkFlowIndent(tok Token, crossed bool) {
	if !crossed || p.flowMinIndent <= 0 {
		return
	}
	switch tok.Type {
	case TokenRSquare, TokenRBrace, TokenComma, TokenEOF:
		return
	}
	if col(tok) < p.flowMinIndent {
		p.fail(tok, fmt.Errorf("flow collection continuation line is under-indented (column %d, need ≥ %d)", col(tok), p.flowMinIndent))
	}
}

// peekIsKeyEnd peeks at the next token without consuming it and reports whether
// it is a valid end-of-implicit-key indicator (EOF, newline, whitespace, or
// comment). Used to decide if a colon is a block mapping value separator.
func (p *parser) peekIsKeyEnd() bool {
	tok := p.rawNext()
	p.back(tok)
	switch tok.Type {
	case TokenEOF, TokenNewline, TokenWhitespace, TokenComment:
		return true
	}
	return false
}

// tryAsBlockMapKey checks whether val (a just-parsed single-line flow
// collection) is being used as an implicit block mapping key. If it is,
// val is wrapped in a new block map and the map is returned. Otherwise val
// is returned unchanged and the look-ahead tokens are restored.
//
// openTok is the '[' or '{' that opened the flow collection; entryCol is the
// column to use as the new block map's indent level.
func (p *parser) tryAsBlockMapKey(flow bool, val Value, openTok Token, entryCol int) Value {
	if flow {
		return val
	}
	// Implicit keys must be single-line (YAML 1.2 spec §6.3.2).
	suffix := val.Base().Suffix
	if len(suffix) > 0 && suffix[len(suffix)-1].Start.Line != openTok.Start.Line {
		return val
	}
	var between []Token
	colonTok := p.skip(&between, TokenWhitespace)
	if colonTok.Type == TokenColon && p.peekIsKeyEnd() {
		val.Base().Suffix = append(append(val.Base().Suffix, between...), colonTok)
		return p.BlockMapContinue(entryCol, val)
	}
	p.back(append(between, colonTok)...)
	return val
}

// valueModifiers holds the anchor/tag properties that may precede a YAML value.
type valueModifiers struct {
	anchor, tag       string
	anchorTok, tagTok Token
	crossedNewline    bool
	// hitSecondAnchor is set when a second anchor is seen after a newline was
	// crossed. This signals that the second anchor belongs to the content (e.g.
	// a block mapping key), not the current node. The caller is responsible for
	// verifying that the content is a *Map; otherwise both anchors alias the same
	// non-Map node, which is invalid per YAML 1.2 spec §6.8.1.
	hitSecondAnchor    bool
	hitSecondAnchorTok Token
}

// parseModifiers reads up to two anchor/tag property tokens from the stream,
// appending them to *leading. It returns the first non-modifier token and
// the collected modifier info.
//
// A duplicate anchor on the same line fails immediately. A duplicate anchor
// after a newline sets mods.hitSecondAnchor so the caller can validate context.
func (p *parser) parseModifiers(tok Token, leading *[]Token) (Token, valueModifiers) {
	var mods valueModifiers
	for i := 0; i < 2; i++ {
		switch tok.Type {
		case TokenAnchor:
			if p.schema.noAnchorsAliases {
				p.fail(tok, fmt.Errorf("anchors are not allowed in strict mode"))
			}
			if mods.anchor != "" {
				if !mods.crossedNewline {
					p.fail(tok, fmt.Errorf("a node may only have one anchor"))
				}
				// Second anchor on a new line: it belongs to the content.
				mods.hitSecondAnchor = true
				mods.hitSecondAnchorTok = tok
				return tok, mods
			}
			mods.anchor = tok.Value.(string)
			mods.anchorTok = tok
			*leading = append(*leading, tok)
			var crossed bool
			tok, crossed = p.skipBlank(leading)
			mods.crossedNewline = mods.crossedNewline || crossed
		case TokenTag:
			if p.schema.rejectExplicitTags {
				p.fail(tok, fmt.Errorf("explicit tags are not allowed in strict mode"))
			}
			p.validateTagHandle(tok, tok.Value.(string))
			mods.tag = tok.Value.(string)
			mods.tagTok = tok
			*leading = append(*leading, tok)
			var crossed bool
			tok, crossed = p.skipBlank(leading)
			mods.crossedNewline = mods.crossedNewline || crossed
		default:
			return tok, mods
		}
	}
	return tok, mods
}
