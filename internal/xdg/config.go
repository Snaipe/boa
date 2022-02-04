// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package xdg

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func ConfigHome() (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", errors.New("could not find the user config home: no $HOME set")
		}
		configHome = filepath.Join(home, ".config")
	}
	return configHome, nil
}

func ConfigDirs() []string {
	configDirs := os.Getenv("XDG_CONFIG_DIRS")
	if configDirs == "" {
		configDirs = "/etc/xdg"
	}
	return strings.Split(configDirs, ":")
}
