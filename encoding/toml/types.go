// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package toml

import "snai.pe/boa/syntax"

// DateTime is a TOML date/time AST node.
// Value holds the parsed time value: one of time.Time, LocalDateTime, LocalDate, or LocalTime.
type DateTime struct {
	syntax.Node
	Value interface{}
}

// KeyPath is a TOML dotted key path used as a MapEntry key.
// It implements syntax.KeyPather.
type KeyPath struct {
	syntax.Node
	Path []interface{}
}

// KeyPathComponents implements syntax.KeyPather.
func (kp *KeyPath) KeyPathComponents() []interface{} { return kp.Path }
