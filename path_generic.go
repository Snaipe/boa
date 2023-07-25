// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

//go:build !darwin && !windows
// +build !darwin,!windows

package boa

import (
	"snai.pe/boa/internal/xdg"
)

func configHome() (string, error) {
	return xdg.ConfigHome()
}

func configDirs() []string {
	var paths []string
	paths = append(paths, "/etc")
	paths = append(paths, xdg.ConfigDirs()...)
	return paths
}

func configPaths() []string {
	paths := configDirs()
	configHome, err := xdg.ConfigHome()
	if err == nil {
		paths = append(paths, configHome)
	}
	return paths
}
