// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

import (
	"io/fs"
	"os"
)

// ConfigHome returns the filesystem path to the current user's configuration
// home, or an error if there is none.
//
// This function should be used to determine where to save configuration. If
// it returns an error, no configuration should be saved.
//
// The configuration home of a user is a os-dependent directory that is
// writeable by that user, and contains configuration files for the programs
// used by that user.
//
// The returned path is generally OS-specific. Typical values per OS are:
//
//   - Linux & UNIX derivatives:  ~/.config  ($XDG_CONFIG_HOME)
//   - macOS:                     ~/Library/Preferences
//   - Windows:                   C:\Users\<user>\AppData\Roaming (%APPDATA%)
func ConfigHome() (string, error) {
	return configHome()
}

// SystemConfigDirs returns the filesystem paths to the system configuration
// directories.
//
// The returned paths are OS-specific. Typical valies per OS are:
//
//   - Linux & UNIX derivatives:  /etc, /etc/xdg ($XDG_CONFIG_DIRS)
//   - macOS:                     /Library/Preferences
//   - Windows:                   C:\ProgramData
func SystemConfigDirs() []string {
	return configDirs()
}

// ConfigPaths returns, in order of least important to most important, the
// paths that may hold configuration files for the current user.
//
// This function should be used to determine from where to load configuration.
// Every matching configuration file in the paths should be loaded into the
// same configuration object, from least important to most important, in
// order to construct the full configuration.
//
// User directories are always more important than system directories. Typical
// values per OS are:
//
//   - Linux & UNIX derivatives:  /etc, /etc/xdg, ~/.config ($XDG_CONFIG_DIRS & $XDG_CONFIG_HOME)
//   - macOS:                     /Library/Preferences, ~/Library/Preferences
//   - Windows:                   C:\ProgramData, C:\Users\<user>\AppData\Roaming
func ConfigPaths() []fs.FS {
	paths := configDirs()
	fs := make([]fs.FS, 0, len(paths)+2)
	if defaultPath != nil {
		fs = append(fs, defaultPath)
	}
	for _, path := range paths {
		fs = append(fs, os.DirFS(path))
	}
	if cfs := ConfigHomeFS(); cfs != nil {
		fs = append(fs, cfs)
	}
	return fs
}

var defaultPath fs.FS

// SetDefaults sets the FS object containing configuration file defaults.
//
// It is added as the least important path in the slice returned by ConfigPaths.
func SetDefaults(defaults fs.FS) {
	defaultPath = defaults
}

var configHomeFS fs.FS

// SetConfigHomeFS overrides the user configuration home, which gets added as
// the most important path in the slice returned by ConfigPaths.
func SetConfigHomeFS(f fs.FS) {
	configHomeFS = f
}

// ConfigHomeFS returns the fs.FS for the user's configuration home.
//
// By default, it returns os.DirFS(ConfigHome()) (or nil if unsuccessful),
// unless SetConfigHomeFS has been called, in which case the FS that was set
// by the function is returned.
func ConfigHomeFS() fs.FS {
	if configHomeFS == nil {
		path, err := configHome()
		if err == nil {
			configHomeFS = os.DirFS(path)
		}
	}
	return configHomeFS
}
