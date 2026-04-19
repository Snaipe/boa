// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package yaml

import (
	"context"
	"io"
	"strings"

	. "snai.pe/boa/syntax"
)

const (
	TokenColon         TokenType = "':'"
	TokenComma         TokenType = "','"
	TokenLBrace        TokenType = "'{'"
	TokenRBrace        TokenType = "'}'"
	TokenLSquare       TokenType = "'['"
	TokenRSquare       TokenType = "']'"
	TokenDash          TokenType = "'-'"
	TokenQuery         TokenType = "'?'"
	TokenDirectivesEnd TokenType = "'---'"
	TokenDocumentEnd   TokenType = "'...'"

	TokenScalar    TokenType = "<yaml:scalar>"
	TokenIndent    TokenType = "<yaml:indent>"
	TokenDirective TokenType = "<yaml:directive>"   // %directive
	TokenTag       TokenType = "<yaml:tag>"         // !, !!, !tag:... or !name!
	TokenBlock     TokenType = "<yaml:block-start>" // | and >
	TokenAnchor    TokenType = "<yaml:anchor>"      // &anchor
	TokenAlias     TokenType = "<yaml:alias>"       // *alias
)

// Multiline string flags
const (
	mlliteral = 1 << (4 + iota)
	mlfold
	mlstrip
	mlkeep

	mlindent = 0xf
)

// flowIndicators is the set of characters that are structural in flow context
// (, [ ] { }). Used to detect where plain scalars and block indicators end.
const flowIndicators = ",[]{}"

func isSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

// emitAll returns a StateFunc chain that emits each token in tokens one at a
// time (one per state-function call), then transitions to next. This works
// around the fixed-size token channel: a single state-function call may only
// emit one token safely.
func emitAll(tokens []Token, next StateFunc) StateFunc {
	if len(tokens) == 0 {
		return next
	}
	return func(l *Lexer) StateFunc {
		l.EmitRaw(tokens[0])
		return emitAll(tokens[1:], next)
	}
}

func isNewline(r rune) bool {
	return r == '\n' || r == '\r'
}

type lexerState struct {

	// Whether we're currently at the start of a line
	newline bool

	// Current indent from start of the line.
	indent int

	// lineIndent is the indentation of the current line (number of leading
	// spaces). It is tentative until a structural token (?, :, -) confirms it
	// as the actual block indent; only then is it copied into indent.
	lineIndent int

	// Set by the parser to true when lexing in flow mode. This only changes
	// the lexing of scalars, since the value of some production can change
	// depending on whether we are in a flow value. For instance:
	//
	//     a, b
	//
	// Can either lex to the "a, b" scalar, unless this is in flow mode, and
	// it instead must lex to "a", "<comma>", "<whitespace>", "b".
	FlowMode bool

	// keyColumn tracks the 0-indexed column where the last mapping key token
	// started. Used to set state.indent correctly when ':' appears in the
	// middle of a line (compact notation such as "- key: value").
	keyColumn int

	// afterJSONKey is true when the last substantive token emitted was a
	// TokenString (quoted scalar). In flow mode, a ':' that follows a JSON key
	// is always the value indicator — no space required — per the YAML spec's
	// c-ns-flow-map-adjacent-value production.
	afterJSONKey bool

	// lastWSHadTab is true when the most recent whitespace token emitted
	// contained a tab character. Used to detect invalid tab separators before
	// block indicators (e.g. "-\t-" is forbidden by YAML 1.2 spec).
	// Cleared at each line boundary (newline token) and at the start of each
	// lex call by capturing the previous value into prevLastWSHadTab.
	lastWSHadTab bool

	// prevTokenWasWS is true when the most recent token emitted was whitespace
	// or a newline. Used to validate that '#' comment markers are preceded by
	// whitespace per YAML 1.2 spec.
	prevTokenWasWS bool
}

// startScalar records the current token column as the key column and
// transitions to plain-scalar lexing. All goto-Scalar sites call this.
func (state *lexerState) startScalar(l *Lexer) StateFunc {
	state.keyColumn = l.TokenPosition.Column - 1
	return state.lexScalar(TokenScalar, mlfold|mlstrip, state.indent)
}

func newLexer(ctx context.Context, input io.Reader) (*Lexer, *lexerState) {
	state := lexerState{
		newline: true,
		indent:  -1, // document level: no parent indentation context
	}
	return NewLexer(ctx, input, state.lex), &state
}

// Flow-collection indicators (,[]{}|>) terminate a tag suffix. Verbatim tags
// (!<uri>) are treated separately so their URI may contain commas.
var reTag = MustCompileRegexp("tag", `^!(?:<[^ \t\r\n>]*>|[^ \t\r\n,\[\]{}|>]*!|tag:[^ \t\r\n]*)?[^ \t\r\n,\[\]{}|>]*`)

func (state *lexerState) lex(l *Lexer) StateFunc {
	r, _, err := l.ReadRune()
	if err != nil {
		return l.Error(err)
	}

	// Capture and clear the tab-in-whitespace flag.  The whitespace case will
	// set it if a tab is found; block-indicator cases will read prevLastWSHadTab
	// to detect invalid tab separators.
	prevLastWSHadTab := state.lastWSHadTab
	state.lastWSHadTab = false

	// Capture whether the previous token was whitespace/newline, for '#' validation.
	prevTokenWasWS := state.prevTokenWasWS
	state.prevTokenWasWS = false

	newline := false
	defer func() {
		state.newline = newline
		if state.newline {
			state.lineIndent = 0
			state.lastWSHadTab = false // tabs don't carry across line boundaries
		}
	}()

	switch r {
	case ' ':
		if state.newline && !state.FlowMode {
			_, err := l.AcceptRun(" ")
			if err != nil {
				return l.Error(err)
			}
			// An indent is only an indent if it precedes any valid token
			// and if we are not in flow mode.
			r, _, err := l.PeekRune()
			if err != io.EOF && !strings.ContainsRune("\r\n#", r) {
				if err != nil {
					return l.Error(err)
				}
				state.lineIndent = len(l.Token())
				l.Emit(TokenIndent, nil)
				state.prevTokenWasWS = true
				return state.lex
			}
		}
		fallthrough
	case '\t':
		// A tab character at the very start of a line is forbidden as indentation
		// inside block structures (YAML 1.2 spec, §6.5).  At document level
		// (state.indent < 0) tabs may precede flow collections as s-separate-in-line.
		if state.newline && r == '\t' {
			if !state.FlowMode && state.indent >= 0 {
				// Only reject if the tab introduces actual content (not a blank line).
				// Read all trailing whitespace first to check.
				if _, err2 := l.AcceptRun(" \t"); err2 != nil {
					return l.Error(err2)
				}
				peek, _, peekErr := l.PeekRune()
				isBlankLine := peekErr == io.EOF || isNewline(peek) || peek == '#'
				if !isBlankLine {
					return l.Errorf("tab character not allowed as indentation in block mode")
				}
				// Tab-only blank line in block mode: emit whitespace and continue.
				state.lastWSHadTab = true
				state.prevTokenWasWS = true
				l.Emit(TokenWhitespace, nil)
				return state.lex
			}
			// In flow mode a tab-only line (followed by whitespace or newline) or a
			// tab before a flow terminator (],},) is tolerated; but a tab that
			// introduces actual scalar content is not allowed.
			if state.FlowMode {
				if peek, _, peekErr := l.PeekRune(); peekErr == nil &&
					!isSpace(peek) && !isNewline(peek) &&
					!strings.ContainsRune(",]}", peek) {
					return l.Errorf("tab character not allowed at start of line before content in flow context")
				}
			}
		}
		ws, err := l.AcceptRun(" \t")
		if err != nil {
			return l.Error(err)
		}
		// Track whether the whitespace sequence included any tab character so
		// that the block-indicator handler can reject "tab as block separator".
		state.lastWSHadTab = r == '\t' || strings.ContainsRune(ws, '\t')
		state.prevTokenWasWS = true
		l.Emit(TokenWhitespace, nil)
	case '\r', '\n':
		if err := l.AcceptNewline(r); err != nil {
			return l.Error(err)
		}
		newline = true
		state.prevTokenWasWS = true // newlines count as whitespace for comment purposes
		l.Emit(TokenNewline, nil)
	case '#':
		// A '#' must be preceded by whitespace (or be at the start of a line)
		// to be a valid comment per YAML 1.2 spec §6.8.
		if !state.newline && !prevTokenWasWS {
			return l.Errorf("comment indicator '#' must be preceded by whitespace")
		}
		comment, err := l.AcceptWhile(func(r rune) bool {
			return !isNewline(r)
		})
		if err != nil {
			return l.Error(err)
		}
		state.prevTokenWasWS = true
		l.Emit(TokenComment, strings.TrimSpace(comment))
	case '-', '.':
		if state.newline {
			// Accept a run of 3 consecutive dashes or dots as a separate token.
			// This handles the directives end token, and the document end token.
			for i := 0; i < 2; i++ {
				next, _, err2 := l.ReadRune()
				if next != r {
					if i == 0 && r == '-' && (err2 == io.EOF || isSpace(next) || isNewline(next)) {
						// A single dash followed by whitespace or EOF is a list marker.
						if err2 == nil {
							l.UnreadRune()
						}
						return state.emitBlockIndicator(l, '-', prevLastWSHadTab)
					}
					return state.startScalar(l)
				}
			}
			next, _, err := l.ReadRune()
			switch {
			case err == io.EOF || isSpace(next) || isNewline(next):
				if err != io.EOF {
					l.UnreadRune()
				}
				if r == '-' {
					l.Emit(TokenDirectivesEnd, nil)
					state.indent = -1 // reset to document level for the new document
				} else {
					l.Emit(TokenDocumentEnd, nil)
					// The document-end marker (...) may only be followed by whitespace
					// or a comment on the same line (YAML 1.2 spec §9.2).
					if isSpace(next) {
						_, _ = l.AcceptRun(" \t")
						peek, _, peekErr := l.PeekRune()
						if peekErr == nil && !isNewline(peek) && peek != '#' {
							return l.Errorf("content not allowed on same line as '...' document-end marker")
						}
						// Emit trailing whitespace so round-trip encoding preserves it.
						l.Emit(TokenWhitespace, nil)
						state.prevTokenWasWS = true
					}
				}
				return state.lex
			case err != nil:
				return l.Error(err)
			default:
				return state.startScalar(l)
			}
		}

		if r != '-' {
			return state.startScalar(l)
		}
		return state.emitBlockIndicator(l, '-', prevLastWSHadTab)

	case '?', ':':
		return state.emitBlockIndicator(l, r, prevLastWSHadTab)

	case '[':
		state.afterJSONKey = false
		l.Emit(TokenLSquare, nil)
	case ']':
		// A closing ']' or '}' may immediately precede ':' as the adjacent-value
		// indicator (c-ns-flow-map-adjacent-value). Set afterJSONKey so the lexer
		// treats ':' as a colon token rather than the start of a plain scalar.
		state.afterJSONKey = true
		l.Emit(TokenRSquare, nil)
	case '{':
		state.afterJSONKey = false
		l.Emit(TokenLBrace, nil)
	case '}':
		state.afterJSONKey = true
		l.Emit(TokenRBrace, nil)
	case ',':
		state.afterJSONKey = false
		l.Emit(TokenComma, nil)
	case '|', '>':
		// Literal and folded scalars. Note: parsing the block header here
		// mixes lexing with parsing slightly — the flags affect how the
		// immediately following scalar token is lexed.

		flags := mlliteral
		if r == '>' {
			flags |= mlfold
		}

		// Parse the optional block-scalar header: up to one chomping indicator
		// ({-, +}) and one indentation indicator ({1-9}), in either order.
		seenChomp, seenIndent := false, false
		for i := 0; i < 2; i++ {
			next, _, err := l.ReadRune()
			if err == io.EOF || isSpace(next) || isNewline(next) {
				if err == nil {
					l.UnreadRune()
				}
				break
			}
			if err != nil {
				return l.Error(err)
			}
			switch {
			case next == '-' || next == '+':
				if seenChomp {
					return l.Errorf("unexpected character %q: expected block indentation indicator", next)
				}
				seenChomp = true
				if next == '-' {
					flags |= mlstrip
				} else {
					flags |= mlkeep
				}
			case next >= '1' && next <= '9':
				if seenIndent {
					return l.Errorf("unexpected character %q: expected block chomping indicator", next)
				}
				seenIndent = true
				flags |= int(next - '0')
			default:
				return l.Errorf("unexpected character %q: expected block chomping or indentation indicator", next)
			}
		}

		// This section mixes lexing with parsing: literal/folded productions
		// may have spacing and comment tokens after the | or >, and the scalar
		// lexer on the next line depends on the flags we just parsed.
		l.Emit(TokenBlock, flags)

		// Collect any trailing whitespace/comment/newline tokens. The token
		// channel has a small buffer, so we emit them one per state-function
		// call via emitAll rather than emitting multiple tokens at once.
		var pending []Token
		stash := func(typ TokenType, val interface{}) {
			pending = append(pending, Token{
				Type:  typ,
				Raw:   l.Token(),
				Value: val,
				Start: l.TokenPosition,
				End:   l.Position,
			})
			l.Discard()
		}

		// Optional spacing
		spacing, err := l.AcceptRun(" ")
		if err != nil {
			return l.Error(err)
		}
		if len(spacing) > 0 {
			stash(TokenWhitespace, nil)
		}

		// Optional comment
		r2, _, err := l.ReadRune()
		if err != nil {
			return l.Error(err)
		}
		if r2 == '#' {
			comment, err := l.AcceptWhile(func(r rune) bool { return !isNewline(r) })
			if err != nil {
				return l.Error(err)
			}
			stash(TokenComment, strings.TrimSpace(comment))
			r2, _, err = l.ReadRune()
			if err != nil {
				return l.Error(err)
			}
		}

		// Mandatory newline after block header
		switch r2 {
		case '\r', '\n':
			if err := l.AcceptNewline(r2); err != nil {
				return l.Error(err)
			}
			newline = true
			stash(TokenNewline, nil)
		default:
			return l.Errorf("unexpected character %q: expected newline", r2)
		}

		return emitAll(pending, state.lexScalar(TokenScalar, flags, state.indent))
	case '%':
		// YAML Directives, but only if starting a line
		if !state.newline {
			return state.startScalar(l)
		}
		directive, err := l.AcceptWhile(func(r rune) bool {
			return !isNewline(r)
		})
		if err != nil {
			return l.Error(err)
		}
		l.Emit(TokenDirective, directive)
	case '*', '&':
		val, err := l.AcceptWhile(func(r rune) bool {
			return !strings.ContainsRune(" \t\r\n,]}", r)
		})
		if err != nil {
			return l.Error(err)
		}
		if r == '&' {
			l.Emit(TokenAnchor, val)
		} else {
			l.Emit(TokenAlias, val)
		}
	case '!':
		// YAML Tags
		l.UnreadRune()
		tag, err := reTag.Accept(l)
		if err != nil {
			return state.startScalar(l)
		}
		// Update keyColumn to the tag's column so that a ':' after a tagged
		// implicit key (e.g. "!!null : val") sets state.indent to the tag's
		// column for correct plain scalar continuation boundary detection.
		state.keyColumn = l.TokenPosition.Column - 1
		l.Emit(TokenTag, tag[0])
	case '\'', '"':
		l.UnreadRune()
		// Track the key column for colon-handling below.
		state.keyColumn = l.TokenPosition.Column - 1
		return state.lexString(r, state.indent, state.FlowMode)
	default:
		return state.startScalar(l)
	}
	return state.lex
}

// emitBlockIndicator handles the common logic for block indicators '?', ':', and '-'.
// All three require a following space (or flow indicator for ':' in flow mode), validate
// tab usage, emit the appropriate token, and update state.indent.
// prevLastWSHadTab is the tab-in-preceding-whitespace flag captured at the top of lex().
func (state *lexerState) emitBlockIndicator(l *Lexer, r rune, prevLastWSHadTab bool) StateFunc {
	next, _, err := l.ReadRune()
	// In flow mode, ':' after a JSON (quoted-string) key is ALWAYS the value
	// indicator — no space required (YAML spec c-ns-flow-map-adjacent-value).
	flowSep := r == ':' && state.FlowMode && (state.afterJSONKey || strings.ContainsRune(flowIndicators, next))
	state.afterJSONKey = false
	if err != io.EOF && !isSpace(next) && !isNewline(next) && !flowSep {
		// Put 'next' back so lexScalar can handle it with its own stop-at-':'
		// logic. This is critical for inputs like '::' where the second ':'
		// followed by a newline/EOF must be treated as the value indicator,
		// not as part of the plain scalar key.
		l.UnreadRune()
		// In flow context, '-' followed by a c-flow-indicator (,[]{}]) is NOT
		// a valid plain-scalar start (ns-plain-first requires '-' to be followed
		// by ns-plain-safe, which excludes c-flow-indicators). Emit TokenDash so
		// the parser can reject it as a block indicator in flow context.
		if r == '-' && state.FlowMode && strings.ContainsRune(flowIndicators, next) {
			l.Emit(TokenDash, nil)
			return state.lex
		}
		return state.startScalar(l)
	}

	// YAML 1.2 spec §6.5: tabs are not valid separators before block content
	// in block (non-flow) context.
	if !state.FlowMode {
		switch r {
		case '?', ':':
			// A tab immediately after ? or : is invalid when it precedes any actual
			// content on the same line (block collection or plain scalar content).
			// Tabs before newline/EOF, block-scalar headers (|, >), comment markers
			// (#), or additional whitespace (space/tab) are allowed.
			if err != io.EOF && next == '\t' {
				peek, _, peekErr := l.PeekRune()
				okAfterTab := peekErr == io.EOF ||
					isNewline(peek) || isSpace(peek) ||
					peek == '|' || peek == '>' ||
					peek == '#'
				if !okAfterTab {
					return l.Errorf("tab character not allowed as block indicator separator")
				}
			}
		case '-':
			// For -, a tab in the *preceding* whitespace is invalid when the next
			// token is also a block indicator (the tab was used as the inter-indicator
			// separator). A tab before a scalar value is tolerated (e.g. "- \t-1" is
			// valid because -1 is a scalar, not a block indicator).
			if prevLastWSHadTab {
				return l.Errorf("tab character not allowed as block indicator separator")
			}
		}
	}

	if err != io.EOF {
		l.UnreadRune()
	}

	// Save the column before emitting (Emit advances TokenPosition past the token).
	tokenCol := l.TokenPosition.Column
	switch r {
	case '?':
		l.Emit(TokenQuery, nil)
	case ':':
		l.Emit(TokenColon, nil)
	case '-':
		l.Emit(TokenDash, nil)
	}

	// This cannot be a plain scalar anymore and therefore the indent we've
	// seen is the actual indentation of the current production.
	switch {
	case r == '-' && !state.newline:
		// Compact nested sequence ("- - item"): '-' itself defines the indent.
		state.indent = tokenCol - 1
	case r == ':' && !state.newline:
		// Implicit mapping key mid-line ("- key: value"): use the column of the
		// key token, not the line's leading whitespace (state.lineIndent).
		state.indent = state.keyColumn
	default:
		state.indent = state.lineIndent
	}
	return state.lex
}

func chompScalar(in string, flags, indent int) string {

	if chomp := flags & mlindent; chomp > 0 {
		indent += chomp // parent indent + explicit indicator = absolute content indent
	} else if flags&mlliteral != 0 {
		// Auto-detect content indent from the first non-empty line.
		// If all lines are blank (spaces only), use the first blank line's
		// indentation — this handles block scalars whose content is entirely
		// empty/trailing blank lines (e.g. |+ with only space-padded blank lines).
		firstBlankSpaces := -1
		for i := 0; i < len(in); {
			end := strings.IndexAny(in[i:], "\r\n")
			var line string
			if end < 0 {
				line = in[i:]
			} else {
				line = in[i : i+end]
			}
			spaces := 0
			for spaces < len(line) && line[spaces] == ' ' {
				spaces++
			}
			if spaces < len(line) {
				indent = spaces
				firstBlankSpaces = -1 // found non-blank; discard blank tracking
				break
			}
			// Blank line: remember the first one's indentation.
			if firstBlankSpaces < 0 && spaces > 0 {
				firstBlankSpaces = spaces
			}
			if end < 0 {
				break
			}
			i += end
			if i < len(in) && in[i] == '\r' {
				i++
			}
			if i < len(in) && in[i] == '\n' {
				i++
			}
		}
		// No non-blank line found: use the first blank line's indentation as
		// the content indent so that the blank lines are correctly identified
		// as "at the content indent" (and thus empty) rather than as content.
		if firstBlankSpaces > 0 {
			indent = firstBlankSpaces
		}
	}

	var out strings.Builder

	// lastMoreIndented tracks whether the last non-blank content flushed was
	// more-indented than the base indent. In a folded scalar, when more-indented
	// content is followed by blank lines and then base-indented content, the
	// more-indented line's literal line break is preserved, so one extra \n is
	// needed compared to the case where base-indented content precedes the blanks.
	lastMoreIndented := false

	// leadingBlanks counts blank lines that appear before any content. They are
	// emitted as \n characters immediately before the first non-blank content line.
	leadingBlanks := 0

	// lastByte tracks the most recently written byte so the fold logic can
	// check whether the last character was a newline, without allocating a
	// string copy of the whole output buffer.
	var lastByte byte

	writeByte := func(b byte) { out.WriteByte(b); lastByte = b }

	flush := func(line string) {
		if out.Len() == 0 {
			if len(line) == 0 {
				// Blank line before any content: defer until first content arrives.
				leadingBlanks++
				return
			}
			// First non-blank content: emit deferred leading blank lines.
			for i := 0; i < leadingBlanks; i++ {
				writeByte('\n')
			}
			lastMoreIndented = line[0] == ' ' || line[0] == '\t'
			out.WriteString(line)
			lastByte = line[len(line)-1]
			return
		}

		if flags&mlfold != 0 {
			switch {
			case len(line) == 0:
				writeByte('\n')
			case !isSpace(rune(line[0])):
				switch {
				case lastByte != '\n' && lastMoreIndented:
					// More-indented line directly followed by base-indented line
					// (no blank line between): preserve the line break literally.
					writeByte('\n')
				case lastByte != '\n':
					// Two consecutive base-indented lines: fold with a space.
					writeByte(' ')
				case lastMoreIndented:
					// More-indented content preserves its line break literally,
					// so when base-indented content follows blank lines that
					// followed more-indented content, we need one extra \n.
					writeByte('\n')
				}
			default:
				writeByte('\n')
			}
		} else {
			writeByte('\n')
		}
		if len(line) > 0 {
			lastMoreIndented = line[0] == ' ' || line[0] == '\t'
			lastByte = line[len(line)-1]
		}
		// Blank lines (len==0) don't change lastMoreIndented or lastByte.
		out.WriteString(line)
	}

	// NOTE: we don't have to use utf8.DecodeRune since we're
	// searching for characters that reside in the range 0x0-0x7f.

	trailingNL := true

	for len(in) > 0 {
		lineend := strings.IndexAny(in, "\r\n")
		if lineend < 0 {
			lineend = len(in)
			trailingNL = false
		}

		line := in[:lineend]
		in = in[lineend:]

		// Skip newline for next loop
		if len(in) > 0 && in[0] == '\r' {
			in = in[1:]
		}
		if len(in) > 0 && in[0] == '\n' {
			in = in[1:]
		}

		if flags&mlliteral != 0 {
			// Block scalar: slice to the detected/explicit content indent.
			if indent >= len(line) {
				flush("")
				continue
			}
			flush(line[indent:])
		} else {
			// Plain scalar: trim leading and trailing spaces/tabs. An all-whitespace
			// line produces "" which flush handles as a blank separator line.
			flush(strings.TrimRight(strings.TrimLeft(line, " \t"), " \t"))
		}
	}

	if trailingNL {
		flush("")
	}

	result := out.String()

	// For mlkeep with only blank lines (no actual content), output the blank lines.
	// Each blank line in the main loop increments leadingBlanks once.
	// If trailingNL is true, one extra flush("") was called at the end, so subtract 1.
	if flags&mlkeep != 0 && len(result) == 0 && leadingBlanks > 0 {
		count := leadingBlanks
		if trailingNL {
			count--
		}
		result = strings.Repeat("\n", count)
	}

	// Process chomping indicators

	switch {
	// Strip
	case flags&mlstrip != 0:
		result = strings.TrimRight(result, " \n")
	// Keep
	case flags&mlkeep != 0:
		break
	// Clip: keep exactly one trailing newline.
	default:
		tmp := strings.TrimRight(result, "\n")
		if len(tmp) == 0 {
			// No content at all: empty scalar.
			result = ""
		} else if len(tmp) == len(result) {
			// Non-empty content but no trailing newline (e.g. file truncated):
			// add exactly one to satisfy the clip invariant.
			result += "\n"
		} else {
			// Trim excess trailing newlines, keep exactly one.
			result = result[:len(tmp)+1]
		}
	}

	return result
}

func (state *lexerState) lexScalar(toktype TokenType, flags, indent int) StateFunc {
	if state.FlowMode {
		indent = -1
	}
	return func(l *Lexer) StateFunc {

		// For block scalars (mlliteral), track the content indent level.
		// -1 means "not yet determined" (auto-detect from first non-blank line).
		// For explicit indent indicators (flags & mlindent > 0), pre-compute it.
		blockContentIndent := -1
		if flags&mlliteral != 0 {
			if explicit := flags & mlindent; explicit > 0 {
				blockContentIndent = indent + explicit
			}
		}

		// maxBlankIndent tracks the maximum indentation seen in blank lines
		// before the first content line of a block scalar. When content indent
		// is finally determined, blank lines with more spaces than content indent
		// are invalid (YAML 1.2 spec §8.1.1.2 l-empty rule).
		maxBlankIndent := 0

		emit := func(raw, curindent string) {

			scalar := Token{
				Type:  toktype,
				Raw:   raw[:len(raw)-len(curindent)],
				Start: l.TokenPosition,
				End:   l.Position,
			}
			scalar.End.Column -= len(curindent)
			scalar.Value = chompScalar(scalar.Raw, flags, indent)

			npos := scalar.End
			npos.Column++

			l.EmitRaw(scalar)

			if len(curindent) > 0 {
				l.EmitRaw(Token{
					Type:  TokenIndent,
					Raw:   raw[len(raw)-len(curindent):],
					Start: npos,
					End:   l.Position,
				})
			}

			l.TokenPosition = l.NextPosition
		}

		// terminateScalar emits the current scalar token up to (but not including)
		// the leading whitespace of the next line, sets state.newline so the lexer
		// resumes in newline mode, and — when curindent is empty — synthesizes a
		// zero-width TokenNewline so the parser's skipBlank registers a line crossing.
		terminateScalar := func(curindent string) StateFunc {
			emit(l.Token(), curindent)
			state.newline = true
			if len(curindent) == 0 {
				npos := l.TokenPosition
				l.EmitRaw(Token{Type: TokenNewline, Start: npos, End: npos})
			}
			return state.lex
		}

		for {
			if state.newline {
				state.newline = false

				curindent, err := l.AcceptRun(" ")
				if err != nil {
					if err == io.EOF {
						emit(l.Token(), "")
					}
					return l.Error(err)
				}

				r1, _, err1 := l.ReadRune()
				if err1 == io.EOF || isNewline(r1) {
					// This is an empty line and shorter indents are ignored.
					if err1 == nil {
						l.UnreadRune() // put the newline back for the next iteration
					}
					// For block scalars, track max blank-line indent before content.
					if flags&mlliteral != 0 && blockContentIndent < 0 && len(curindent) > maxBlankIndent {
						maxBlankIndent = len(curindent)
					}
					continue
				}
				if err1 != nil {
					return l.Error(err1)
				}
				// For block scalars: a tab at the start of a line (no leading spaces)
				// before the content indent is determined is invalid — block scalar
				// indentation must use spaces only (YAML 1.2 spec §8.1.1).
				if flags&mlliteral != 0 && r1 == '\t' && len(curindent) == 0 && blockContentIndent < 0 {
					return l.Errorf("tab character not allowed as indentation in block scalar")
				}

				// At document level (indent == -1), check for document markers
				// (--- or ...) at column 0 which terminate the scalar.
				if indent < 0 && len(curindent) == 0 && (r1 == '-' || r1 == '.') {
					// r1 is already read; peek the next 3 chars to check for a marker.
					rest, _ := l.PeekPrefix(3)
					isMarker := len(rest) >= 2 && rune(rest[0]) == r1 && rune(rest[1]) == r1 &&
						(len(rest) < 3 || isSpace(rune(rest[2])) || isNewline(rune(rest[2])))
					l.UnreadRune() // put r1 back regardless
					if isMarker {
						emit(l.Token(), "")
						state.newline = true
						return state.lex
					}
				} else {
					// A '#' at the start of a new line terminates a plain scalar.
					if r1 == '#' && flags&mlliteral == 0 {
						l.UnreadRune() // put '#' back for lexer to emit as comment
						emit(l.Token(), curindent)
						state.newline = true
						return state.lex
					}
					l.UnreadRune() // unread r1; not a marker, let the main loop read it
				}

				if flags&mlliteral != 0 {
					// Block scalar: use content indent for termination, not parent indent.
					if blockContentIndent < 0 {
						// First non-blank content line: auto-detect content indent.
						// If the line is at or below the parent indent, the block
						// scalar has no content — terminate immediately.
						if indent >= 0 && len(curindent) <= indent {
							return terminateScalar(curindent)
						}
						blockContentIndent = len(curindent)
						// Blank lines before the first content line must not have
						// more spaces than the content indent (YAML 1.2 §8.1.1.2).
						if maxBlankIndent > blockContentIndent {
							return l.Errorf("block scalar: leading blank lines have more indentation than content")
						}
					} else if len(curindent) < blockContentIndent {
						// Non-blank line at lower indent than content: end of block scalar.
						return terminateScalar(curindent)
					}
				} else if indent >= 0 && len(curindent) <= indent {
					// Plain scalar: terminate at parent indent or shorter.
					return terminateScalar(curindent)
				}
			}

			// Consume until end of line, or we reach ": " or " #".

			r, _, err := l.ReadRune()
			switch {
			case err == io.EOF:
				emit(l.Token(), "")
				return l.Error(err)
			case err != nil:
				return l.Error(err)
			}

			switch r {

			case '\r', '\n':
				if err := l.AcceptNewline(r); err != nil {
					return l.Error(err)
				}
				state.newline = true
				continue
			case ']', '}':
				if state.FlowMode {
					l.UnreadRune()
					emit(l.Token(), "")
					return state.lex
				}
			case ',':
				if !state.FlowMode {
					break
				}
				// In flow mode, comma always terminates a plain scalar.
				l.UnreadRune()
				emit(l.Token(), "")
				return state.lex
			case ' ', '\t', ':':
				next, _, err := l.PeekRune()
				if err != nil {
					if err == io.EOF {
						emit(l.Token(), "")
					}
					return l.Error(err)
				}
				var stop bool
				if isSpace(r) {
					// Space or tab stops the scalar before a comment marker,
					// but only in plain scalars (not block scalars).
					stop = next == '#' && flags&mlliteral == 0
				} else {
					// Colon stops before space (including tab), newline, or (in flow mode) flow indicators.
					stop = isSpace(next) || isNewline(next) ||
						(state.FlowMode && strings.ContainsRune(flowIndicators, next))
				}
				if stop {
					l.UnreadRune()
					emit(l.Token(), "")
					return state.lex
				}
			}
		}
	}
}

func (state *lexerState) lexString(delim rune, blockIndent int, inFlow bool) StateFunc {
	return func(l *Lexer) StateFunc {
		var val strings.Builder
		// pendingWS accumulates literal (non-escaped) trailing whitespace.
		// It is discarded when a newline is folded, and flushed to val otherwise.
		var pendingWS strings.Builder
		flushPendingWS := func() {
			if pendingWS.Len() > 0 {
				val.WriteString(pendingWS.String())
				pendingWS.Reset()
			}
		}
		// minIndent is the minimum number of spaces a continuation line must
		// start with in block (non-flow) context. Per YAML 1.2 spec §7.3.1/7.3.3,
		// s-flow-line-prefix(n) = s-indent(n) which requires at least n spaces,
		// where n = blockIndent+1. In flow context indentation is not enforced.
		minIndent := blockIndent + 1
		if inFlow || minIndent < 0 {
			minIndent = 0
		}
		if _, err := l.AcceptRune(delim); err != nil {
			return l.Error(err)
		}

		for {
			r, _, err := l.ReadRune()
			if err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return l.Error(err)
			}

			switch r {
			case delim:
				// In single-quoted strings, a repeated quote ('') may be used
				// to actually insert a literal single quote. It therefore
				// does not mark the end of string.

				next, _, err := l.ReadRune()
				if next == delim {
					flushPendingWS()
					val.WriteRune(r)
					break
				}

				if err == nil {
					l.UnreadRune()
				}
				if err != nil && err != io.EOF {
					return l.Error(err)
				}

				flushPendingWS()
				l.Emit(TokenString, val.String())
				// A quoted string may be used as a JSON mapping key; in flow mode
				// the subsequent ':' can be adjacent (no space required).
				state.afterJSONKey = true
				return state.lex
			case '\n', '\r':
				// Multi-line quoted scalar: fold newlines per YAML spec.
				// Discard trailing literal whitespace (pendingWS) accumulated since
				// the last content; escaped whitespace was already flushed to val.
				pendingWS.Reset()
				if r == '\r' {
					l.SkipOptionalLF()
				}
				emptyLines := 0
			foldLoop:
				for {
					// Skip leading whitespace on the next line.
					// Track whether we are still at the very start of the line
					// (no spaces read yet) and how many spaces have been consumed.
					lineSpaces := 0
					startOfLine := true
					for {
						c, _, err2 := l.ReadRune()
						if err2 != nil {
							break foldLoop
						}
						if c == '\t' && startOfLine && minIndent > 0 {
							// Tab as the very first character on a continuation line is
							// invalid when indentation is required (minIndent > 0).
							// Per YAML 1.2 spec §7.3.1: s-flow-line-prefix = s-indent(n)
							// (spaces only) + optional s-separate-in-line. At document
							// level (minIndent=0) a leading tab is allowed.
							return l.Errorf("tab character not allowed as first indentation character in quoted scalar")
						}
						if !isSpace(c) {
							l.UnreadRune()
							break
						}
						startOfLine = false
						if c == ' ' {
							lineSpaces++
						}
					}
					// Peek for another newline (empty line) or real content.
					c, _, err2 := l.ReadRune()
					if err2 != nil {
						break
					}
					if isNewline(c) {
						if c == '\r' {
							l.SkipOptionalLF()
						}
						emptyLines++
					} else {
						l.UnreadRune()
						// Real content — apply doc-marker and indent checks.
						// Per YAML 1.2 spec §7.3.1/7.3.3: document markers
						// (--- and ...) at column 0 always terminate the document,
						// even inside a quoted scalar — leaving the string unterminated.
						if l.NextPosition.Column == 1 {
							head, _ := l.PeekPrefix(4)
							if len(head) >= 3 &&
								head[0] == head[1] && head[1] == head[2] &&
								(head[0] == '-' || head[0] == '.') &&
								(len(head) < 4 || isSpace(rune(head[3])) || isNewline(rune(head[3]))) {
								return l.Errorf("unterminated quoted scalar: document marker '%c%c%c' terminates document", head[0], head[1], head[2])
							}
						}
						// In block context, continuation lines must be indented
						// to at least the flow scalar's context level (blockIndent+1).
						if !inFlow && lineSpaces < minIndent {
							return l.Errorf("continuation line of quoted scalar must be indented to at least column %d", minIndent+1)
						}
						break
					}
				}
				if emptyLines == 0 {
					val.WriteByte(' ')
				} else {
					for i := 0; i < emptyLines; i++ {
						val.WriteByte('\n')
					}
				}
			case '\\':
				// Backslash-escaping is only performed in double-quoted strings
				if delim == '\'' {
					flushPendingWS()
					val.WriteRune(r)
					break
				}
				// Flush any pending literal whitespace before processing the escape;
				// the escape result is content (not trailing whitespace to be discarded).
				flushPendingWS()
				next, _, err := l.ReadRune()
				if err != nil {
					if err == io.EOF {
						err = io.ErrUnexpectedEOF
					}
					return l.Error(err)
				}

				switch next {
				case '\n', '\r':
					// Escaped newline (or CRLF): line continuation — discard the newline
					// and all leading whitespace on the next line.
					if next == '\r' {
						l.SkipOptionalLF()
					}
					_, _ = l.AcceptRun(" \t")
				case '\\', delim, ' ', '\t', '/':
					val.WriteRune(next)
				case '0':
					val.WriteRune('\x00')
				case 'a':
					val.WriteRune('\a')
				case 'b':
					val.WriteRune('\b')
				case 'f':
					val.WriteRune('\f')
				case 'n':
					val.WriteRune('\n')
				case 'r':
					val.WriteRune('\r')
				case 't':
					val.WriteRune('\t')
				case 'v':
					val.WriteRune('\v')
				case 'e':
					val.WriteRune('\x1b')
				case 'N':
					val.WriteRune('\x85')
				case '_':
					val.WriteRune('\xa0')
				case 'L':
					val.WriteRune('\u2028')
				case 'P':
					val.WriteRune('\u2029')
				case 'x', 'u', 'U':
					var length int
					switch next {
					case 'x':
						length = 2
					case 'u':
						length = 4
					case 'U':
						length = 8
					}
					codepoint, err := l.ParseUnicodeEscape(length)
					if err != nil {
						return l.Error(err)
					}
					val.WriteRune(codepoint)
				default:
					// Single-quoted strings exit early above; this point is only
					// reached for double-quoted strings. Any unrecognised escape
					// sequence is an error per YAML 1.2 §8.1.1.2.
					return l.Errorf("invalid escape sequence '\\%c'", next)
				}
			default:
				if isSpace(r) {
					// Literal whitespace: accumulate as pending (may be discarded at \n fold).
					pendingWS.WriteRune(r)
				} else {
					flushPendingWS()
					val.WriteRune(r)
				}
			}
		}
	}
}
