// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

import (
	"testing"
)

func TestNoFilesWithEnv(t *testing.T) {
	// Test that the automatic population from environment variables
	// still work when there are no matching configs

	type Config struct {
		Prefixed   string
		Unprefixed string `env:"UNPREFIXED"`
	}

	SetOptions(
		AutomaticEnv("BOA"),
		Environ([]string{"BOA_PREFIXED=foo", "UNPREFIXED=bar"}),
	)

	var config Config

	err := Load("boa_can_never_match", &config)
	if err != nil {
		t.Fatal(err)
	}

	if config.Prefixed != "foo" {
		t.Fatalf("expected foo, got %q", config.Prefixed)
	}
	if config.Unprefixed != "bar" {
		t.Fatalf("expected bar, got %q", config.Unprefixed)
	}
}
