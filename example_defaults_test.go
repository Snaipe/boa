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

// This embed declaration embeds the example_defaults.toml file (relative to
// the package directory) into the filesystem at `defaults`.

//go:embed example_defaults.*
var defaults embed.FS

var config struct {
	Greeting string `help:"A nice hello."`
}

func Example() {

	// Register the default files
	boa.SetDefaultsPath(defaults)

	// Opens and loads, in order, the example_defaults.toml files from the
	// following paths:
	//
	//     -  <defaults>/example_defaults.toml
	//     -  /etc/example_defaults.toml
	//     -  ~/.config/example_defaults.toml
	//
	cfg := boa.Open("example_defaults.toml")
	defer cfg.Close()

	// Load the defaults into the config variable
	if err := boa.NewDecoder(cfg).Decode(&config); err != nil {
		log.Fatalln(err)
	}

	fmt.Println(config.Greeting)
	// Output: Hello from TOML!
}
