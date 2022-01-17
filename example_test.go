// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa_test

import (
	"fmt"
	"log"

	"snai.pe/boa"
)

func ExampleLoad() {

	var config struct {
		Answer   int               `help:"This is an important field that needs to be 42"`
		Primes   []int             `help:"Some prime numbers"`
		Contacts map[string]string `help:"Some people in my contact list"`
	}

	if err := boa.Load("testdata/example.json5", &config); err != nil {
		log.Fatalln(err)
	}

	fmt.Println("answer:", config.Answer)
	fmt.Println("primes:", config.Primes)
	fmt.Println("contacts:", config.Contacts)
	// Output: answer: 42
	// primes: [2 3 5 7 11]
	// contacts: map[alice:alice@example.com bob:bob@example.com snaipe:me@snai.pe]
}
