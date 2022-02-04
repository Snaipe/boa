// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

import (
	"errors"
	"os"
	"path/filepath"
)

func configHome() (string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return "", errors.New("could not find the user config home: no $HOME set")
	}
	return filepath.Join(home, "Library", "Preferences"), nil
}

func configPaths() []string {
	paths := make([]string, 0, 2)
	paths = append(paths, "/Library/Preferences")
	configHome, err := ConfigHome()
	if err == nil {
		paths = append(paths, configHome)
	}
	return paths
}
