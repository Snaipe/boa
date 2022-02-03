// Copyright 2022 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package testutil

import (
	"os/exec"
	"strings"
	"testing"
)

func GitDiffNoIndex(t *testing.T, path1, path2 string) {
	t.Helper()

	var out strings.Builder
	cmd := exec.Command("git", "diff", "--color=always", "--no-index", path1, path2)
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()

	t.Log(out.String())
	if err != nil {
		t.Fatal(err)
	}
}
