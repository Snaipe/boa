<h1 align="center"><img src="assets/title.svg" height="200" alt="Boa Logo" /></h1>

A friendlier viper.

[![GoDoc](https://godoc.org/snai.pe/boa?status.svg)](https://godoc.org/snai.pe/boa)  

```
go get snai.pe/boa
```

## Introduction

Boa aims to be a **simple, human-focused, no-dependency** configuration management library.

It supports **JSON5** and **TOML**.

Configurations are expressed as Go types, using struct field tags to map configuration files
to type-safe data types.

For instance, the following TOML configuration file:

```toml
[person]
first-name = "John"
last-name = "Doe"
dob = 1953-02-25T00:00:42-08:00
```

Can be loaded into this Go type:

```go
type Config struct {
  Person struct {
    FirstName string
    LastName  string
    Birth     time.Time `name:"dob"`
  } `naming:"kebab-case"`
}
```

## Why boa?

At the time of writing, none of the other configuration parsers are actually designed
for configuration. Most of the standard parsers under the `encoding` package are
designed for robots. Non-standard parsers either do not follow the same semantics
as the standard packages, or suffer the same flaws as standard parsers. Comments
and formatting are usually not parsed nor preserved. Encoders interact poorly
with other encoders, usually necessitating multiple struct tags per configuration
language, or do not provide a good interface for accessing and discovering
configuration values.

Boa aims to be an overall better configuration management library. It has _no_
dependencies outside of the standard Go library, and provides a unified way to load,
manipulate, and save configurations.

The following languages are supported:

* JSON5
* TOML

In addition, all configuration parsers have the following properties:

* Error messages contain the filename when available as well as the line and column
  number where the error occured.
* Parsers support the same set of base struct tags for consistency and conciseness.
* Comments and whitespace are preserved by the parsers in the configuration AST,
  which makes it possible to edit configuration while still preserving the style
  of the original file.

## Supported tags

<!--
NOTE: The values in the Tag column below contain an invisible word-joiner character
(U+2060) after the first double-quote (") character. This is done to prevent
any unfortunate line breaks in the middle of the escaped value, but might mess up
some editors.

To insert these characters in vim, position the cursor where the character needs
to be inserted, enter insert mode, press Ctrl-V, then u2060.
-->

| Tag               | Description
|-------------------|-------------
| `name:"⁠<name>"`   | Set key name.
| `help:"⁠<help>"`   | Set documentation; appears as comment in the config.
| `naming:"⁠<name>"` | Set naming convention for key and subkeys.
| `inline`          | Inline field. All sub-fields will be treated as if they were in the containing struct itself. Does the same as embedding the field.
| `-`               | Ignore field.

## Supported types

Any type that implements [`encoding.TextMarshaler`][encoding.TextMarshaler] can be saved as a string.
Any type that implements [`encoding.TextUnmarshaler`][encoding.TextUnmarshaler] can be loaded from a string.

In addition, the following standard library types are marshaled and unmarshaled as the appropriate type:

| Type                 | Treated as |
|----------------------|------------|
| `[]byte`             | String     |
| `*big.Int`           | Number     |
| `*big.Float`         | Number     |
| `*big.Rat`           | Number     |
| `time.Time`          | String     |
| `*url.URL`           | String     |

Some packages also define or support some specialized types for specific configuration objects:

| Type                 | Treated as | Packages (under `snai.pe/boa/encoding`)
|----------------------|------------|-----------------------------------------
| `time.Time`          | DateTime   | `toml`
| `toml.LocalDateTime` | DateTime   | `toml`
| `toml.LocalDate`     | DateTime   | `toml`
| `toml.LocalTime`     | DateTime   | `toml`

## Examples

### Loading configuration

```golang
package main

import (
	"fmt"
	"log"

	"snai.pe/boa"
)

func main() {

	var config struct {
		Answer   int               `help:"This is an important field that needs to be 42"`
		Primes   []int             `help:"Some prime numbers"`
		Contacts map[string]string `help:"Some people in my contact list"`
	}

	// Will load any matching "appname.toml" config file from the system config path,
	// then the user config path. The TOML decoder is inferred from the .toml extension.
	//
	// For instance, on Linux, this will load in order:
	//     - /etc/<appname>.toml
	//     - /etc/xdg/<appname>.toml
	//     - ~/.config/<appname>.toml
	//
	if err := boa.Load("appname", &config); err != nil {
		log.Fatalln(err)
	}

}
```

### Loading configuration, with defaults

Configuration defaults are not, by design, set via struct tags or other field-specific mechanisms.

Instead, write a default configuration file in your package, and embed it. Multiple configs
defaults can be embedded into the same embed.FS declaration -- see the documentation of
the [embed](https://pkg.go.dev/embed) package.

```golang
package main

import (
	"embed"
	"fmt"
	"log"

	"snai.pe/boa"
)

//go:embed appname.toml
var defaults embed.FS

func main() {

	// Register defaults
	boa.SetDefaultsPath(defaults)

	var config struct {
		Answer   int               `help:"This is an important field that needs to be 42"`
		Primes   []int             `help:"Some prime numbers"`
		Contacts map[string]string `help:"Some people in my contact list"`
	}

	if err := boa.Load("appname", &config); err != nil {
		log.Fatalln(err)
	}

}
```

Good configuration defaults should be consistent and self-explanatory. Consider making
the default for fields their respective type's zero value.

## Credits

Logo made by [Irina Mir](https://twitter.com/irmirx)

[encoding.TextMarshaler]: https://pkg.go.dev/encoding#TextMarshaler
[encoding.TextUnmarshaler]: https://pkg.go.dev/encoding#TextUnmarshaler
