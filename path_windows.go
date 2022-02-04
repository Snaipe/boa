// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

import (
	"errors"
	"os"
)

func configHome() (string, error) {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		return "", errors.New("could not find the user config home: no %APPDATA% set")
	}
	return appdata, nil
}

func configPaths() []string {
	paths := make([]string, 0, 2)
	paths = append(paths, `C:\ProgramData`)
	configHome, err := ConfigHome()
	if err == nil {
		paths = append(paths, configHome)
	}
	return paths
}
