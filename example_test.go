// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa_test

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"snai.pe/boa"
)

func ExampleDecoder() {

	var config struct {
		Answer   int               `help:"This is an important field that needs to be 42"`
		Primes   []int             `help:"Some prime numbers"`
		Contacts map[string]string `help:"Some people in my contact list"`
	}

	f, err := os.Open("testdata/example.json5")
	if err != nil {
		log.Fatalln(err)
	}
	defer f.Close()

	if err := boa.NewDecoder(f).Decode(&config); err != nil {
		log.Fatalln(err)
	}

	fmt.Println("answer:", config.Answer)
	fmt.Println("primes:", config.Primes)
	fmt.Println("contacts:", config.Contacts)
	// Output: answer: 42
	// primes: [2 3 5 7 11]
	// contacts: map[alice:alice@example.com bob:bob@example.com snaipe:me@snai.pe]
}

func ExampleEncoder_toml() {

	type Person struct {
		Name string
		DOB  time.Time
	}

	type Database struct {
		Server string `help:"Database endpoint; can be one of:
						* IPv4
						* IPv6
						* DNS host name."`

		Ports         []uint16 `help:"Database ports, in the range [1, 65535)."`
		ConnectionMax int      `help:"Maximum number of connections."`
		Enabled       bool
	}

	type Server struct {
		IP string
		DC string
	}

	type Config struct {
		Title    string
		Owner    Person
		Database Database
		Servers  map[string]Server `help:"Set of servers. Each server has a name, an IP, and a datacenter name."`

		// This field is ignored
		Ignored string `-`

		ForeignKeys struct {
			SomeInt int
		} `naming:"kebab-case" help:"This table has a different naming convention"`
	}

	config := Config{
		Title: "TOML Example",

		Owner: Person{
			Name: "Snaipe",
			DOB:  time.Date(1979, 05, 27, 07, 32, 00, 0, time.FixedZone("", -8*60*60)),
		},

		Database: Database{
			Server:        "192.168.1.1",
			Ports:         []uint16{8001, 8001, 8002},
			ConnectionMax: 5000,
			Enabled:       true,
		},

		Servers: map[string]Server{
			"alpha": Server{
				IP: "10.0.0.1",
				DC: "eqdc10",
			},
			"beta": Server{
				IP: "10.0.0.2",
				DC: "eqdc10",
			},
		},
	}

	f, err := os.Create("testdata/example_save.toml")
	if err != nil {
		log.Fatalln(err)
	}
	defer f.Close()

	if err := boa.NewEncoder(f).Encode(config); err != nil {
		log.Fatalln(err)
	}

	out, err := ioutil.ReadFile("testdata/example_save.toml")
	if err != nil {
		log.Fatalln(err)
	}
	os.Stdout.Write(out)

	// Output: title = "TOML Example"
	//
	// [owner]
	// name = "Snaipe"
	// dob = 1979-05-27T07:32:00-08:00
	//
	// [database]
	// # Database endpoint; can be one of:
	// # * IPv4
	// # * IPv6
	// # * DNS host name.
	// server = "192.168.1.1"
	// # Database ports, in the range [1, 65535).
	// ports = [
	//   8001,
	//   8001,
	//   8002,
	// ]
	// # Maximum number of connections.
	// connection_max = 5000
	// enabled = true
	//
	// # Set of servers. Each server has a name, an IP, and a datacenter name.
	// [servers]
	//
	//   [servers.alpha]
	//   ip = "10.0.0.1"
	//   dc = "eqdc10"
	//
	//   [servers.beta]
	//   ip = "10.0.0.2"
	//   dc = "eqdc10"
	//
	// # This table has a different naming convention
	// [foreign-keys]
	// some-int = 0
}

func ExampleEncoder_json5() {

	type Person struct {
		Name  string
		Email string
	}

	type Database struct {
		Server string `help:"Database endpoint; can be one of:
						* IPv4
						* IPv6
						* DNS host name."`

		Ports         []uint16 `help:"Database ports, in the range [1, 65535)."`
		ConnectionMax int      `help:"Maximum number of connections."`
		Enabled       bool
	}

	type Server struct {
		IP string
		DC string
	}

	type Config struct {
		Title    string
		Owner    Person
		Database Database
		Servers  map[string]Server `help:"Set of servers. Each server has a name, an IP, and a datacenter name."`

		// This field is ignored
		Ignored string `-`

		ForeignKeys struct {
			SomeInt int
		} `naming:"kebab-case" help:"This map has a different naming convention"`
	}

	config := Config{
		Title: "JSON5 Example",

		Owner: Person{
			Name:  "Snaipe",
			Email: "me@snai.pe",
		},

		Database: Database{
			Server:        "192.168.1.1",
			Ports:         []uint16{8001, 8001, 8002},
			ConnectionMax: 5000,
			Enabled:       true,
		},

		Servers: map[string]Server{
			"alpha": Server{
				IP: "10.0.0.1",
				DC: "eqdc10",
			},
			"beta": Server{
				IP: "10.0.0.2",
				DC: "eqdc10",
			},
		},

		Ignored: "this field is ignored",
	}

	f, err := os.Create("testdata/example_save.json5")
	if err != nil {
		log.Fatalln(err)
	}
	defer f.Close()

	if err := boa.NewEncoder(f).Encode(config); err != nil {
		log.Fatalln(err)
	}

	out, err := ioutil.ReadFile("testdata/example_save.json5")
	if err != nil {
		log.Fatalln(err)
	}
	os.Stdout.Write(out)

	// Output: {
	//   title: "JSON5 Example",
	//   owner: {
	//     name: "Snaipe",
	//     email: "me@snai.pe",
	//   },
	//   database: {
	//     // Database endpoint; can be one of:
	//     // * IPv4
	//     // * IPv6
	//     // * DNS host name.
	//     server: "192.168.1.1",
	//     // Database ports, in the range [1, 65535).
	//     ports: [
	//       8001,
	//       8001,
	//       8002,
	//     ],
	//     // Maximum number of connections.
	//     connectionMax: 5000,
	//     enabled: true,
	//   },
	//   // Set of servers. Each server has a name, an IP, and a datacenter name.
	//   servers: {
	//     alpha: {
	//       ip: "10.0.0.1",
	//       dc: "eqdc10",
	//     },
	//     beta: {
	//       ip: "10.0.0.2",
	//       dc: "eqdc10",
	//     },
	//   },
	//   // This map has a different naming convention
	//   "foreign-keys": {
	//     "some-int": 0,
	//   },
	// }
}

func ExampleSetOptions() {

	type Config struct {
		FirstName string
		LastName  string
		Nickname  string
	}

	config := Config{
		FirstName: "Franklin",
		LastName:  "Mathieu",
		Nickname:  "Snaipe",
	}

	boa.SetOptions(
		boa.Indent("\t"),
		boa.NamingConvention("kebab-case"),
	)

	f, err := os.Create("testdata/example_opts.json5")
	if err != nil {
		log.Fatalln(err)
	}
	defer f.Close()

	if err := boa.NewEncoder(f).Encode(config); err != nil {
		log.Fatalln(err)
	}

	out, err := ioutil.ReadFile("testdata/example_opts.json5")
	if err != nil {
		log.Fatalln(err)
	}
	os.Stdout.Write(out)

	// Output:
	// {
	// 	"first-name": "Franklin",
	// 	"last-name": "Mathieu",
	// 	nickname: "Snaipe",
	// }
}

func ExampleAutomaticEnv() {

	type Config struct {
		// Explicitly set by PATH variable. Does not need AutomaticEnv.
		Path []string `env:"PATH"`

		// Implicitly defined by BOA_SHELL due to AutomaticEnv("BOA") option.
		Shell string
	}

	environ := []string{
		"PATH=" + strings.Join([]string{"/bin", "/usr/bin", "/sbin", "/usr/sbin"}, string(os.PathListSeparator)),
		"BOA_SHELL=/bin/sh",
	}

	boa.SetOptions(
		boa.AutomaticEnv("BOA"),
		boa.Environ(environ),
	)

	var config Config

	f, err := os.Open("testdata/example.json5")
	if err != nil {
		log.Fatalln(err)
	}
	defer f.Close()

	if err := boa.NewDecoder(f).Decode(&config); err != nil {
		log.Fatalln(err)
	}

	fmt.Println("Path:", config.Path)
	fmt.Println("Shell:", config.Shell)

	// Output:
	// Path: [/bin /usr/bin /sbin /usr/sbin]
	// Shell: /bin/sh
}
