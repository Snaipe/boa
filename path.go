// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package boa

// ConfigHome returns the path to the current user's configuration home, or
// an error if there is none.
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
//     - Linux & UNIX derivatives:  ~/.config  ($XDG_CONFIG_HOME)
//     - macOS:                     ~/Library/Preferences
//     - Windows:                   C:\Users\<user>\AppData\Roaming (%APPDATA%)
//
func ConfigHome() (string, error) {
	return configHome()
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
//     - Linux & UNIX derivatives:  /etc, /etc/xdg, ~/.config ($XDG_CONFIG_DIRS & $XDG_CONFIG_HOME)
//     - macOS:                     /Library/Preferences, ~/Library/Preferences
//     - Windows:                   C:\ProgramData, C:\Users\<user>\AppData\Roaming
func ConfigPaths() []string {
	return configPaths()
}
