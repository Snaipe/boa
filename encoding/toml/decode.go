// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package toml

import (
	"io"
	"os"
	"reflect"
	"time"

	. "snai.pe/boa/syntax"

	"snai.pe/boa/encoding"
	"snai.pe/boa/internal/reflectutil"

	"fmt"
)

type Decoder struct {
	parser *parser
}

func NewDecoder(rd io.Reader) *Decoder {
	type namer interface {
		Name() string
	}
	name := ""
	if namer, ok := rd.(namer); ok {
		name = namer.Name()
	}
	decoder := Decoder{
		parser: newParser(name, rd),
	}
	return &decoder
}

func (decoder *Decoder) Decode(v interface{}) error {
	root, err := decoder.parser.Parse()
	if err != nil {
		return err
	}
	if node, ok := v.(**Node); ok {
		*node = root
		return nil
	}
	ptr := reflect.ValueOf(v)
	if ptr.Kind() != reflect.Ptr {
		panic("toml.Decoder.Decode: must pass in pointer value")
	}

	toTime := func(v interface{}) time.Time {
		switch t := v.(type) {
		case time.Time:
			return t
		case LocalDate:
			return t.Time(0, 0, 0, 0, time.Local)
		case LocalTime:
			now := time.Now()
			return t.Time(now.Year(), now.Month(), now.Day(), time.Local)
		case LocalDateTime:
			return t.Time(time.Local)
		default:
			panic(fmt.Sprintf("unknown datetime node value type %T", t))
		}
	}

	err = reflectutil.Populate(ptr.Elem(), root.Child, encoding.CamelCase, func(val reflect.Value, node *Node) (bool, error) {

		newNodeErr := func(exp NodeType) error {
			return fmt.Errorf("config has %v, but expected %v instead", node.Type, exp)
		}

		switch val.Interface().(type) {
		case time.Time:
			if node.Type != NodeDateTime {
				return false, newNodeErr(NodeDateTime)
			}
			val.Set(reflect.ValueOf(toTime(node.Value)))
			return true, nil
		case LocalDate, LocalTime, LocalDateTime:
			if node.Type != NodeDateTime {
				return false, newNodeErr(NodeDateTime)
			}
			rval := reflect.ValueOf(node.Value).Elem()
			if rval.Type().AssignableTo(val.Type()) {
				val.Set(rval)
				return true, nil
			}
			if t, ok := node.Value.(time.Time); ok {
				switch val.Interface().(type) {
				case LocalDate:
					val.Set(reflect.ValueOf(MakeLocalDate(t)))
				case LocalTime:
					val.Set(reflect.ValueOf(MakeLocalTime(t)))
				case LocalDateTime:
					val.Set(reflect.ValueOf(MakeLocalDateTime(t)))
				}
				return true, nil
			}
		}
		return false, nil
	})
	if e, ok := err.(*encoding.LoadError); ok {
		e.Filename = decoder.parser.name
	}
	return err
}

// Load is a convenience function to load a JSON5 document into the value
// pointed at by v. It is functionally equivalent to NewDecoder(<file at path>).Decode(v).
func Load(path string, v interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return NewDecoder(f).Decode(v)
}