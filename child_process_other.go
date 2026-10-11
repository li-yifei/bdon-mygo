//go:build !windows

package main

import "os/exec"

// configureChildProcess has nothing to do outside Windows.
func configureChildProcess(*exec.Cmd) {}
