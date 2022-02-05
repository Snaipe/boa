// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa_test

import (
	"embed"
	"fmt"
	"log"

	"snai.pe/boa"
)

// This embed declaration embeds the example_defaults.toml (relative to
// the package directory) into the filesystem at `defaults`.

//go:embed example_defaults.toml
var defaults embed.FS

var config struct {
	Greeting string `help:"A nice hello."`
}

func Example() {

	// Open the default configuration embedded in the defaults FS object
	cfg, err := defaults.Open("example_defaults.toml")
	if err != nil {
		log.Fatalln(err)
	}

	// Load the defaults into the config variable
	if err := boa.NewDecoder(cfg).Decode(&config); err != nil {
		log.Fatalln(err)
	}

	fmt.Println(config.Greeting)
	// Output: Hello from TOML!
}
