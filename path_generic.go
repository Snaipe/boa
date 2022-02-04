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

func configPaths() []string {
	var paths []string
	for _, p := range xdg.ConfigDirs() {
		paths = append(paths, p)
	}
	configHome, err := xdg.ConfigHome()
	if err == nil {
		paths = append(paths, configHome)
	}
	return paths
}
