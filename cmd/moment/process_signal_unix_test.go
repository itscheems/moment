//go:build !windows

package main

import (
	"os"
	"os/exec"
)

func configureInterruptProcess(*exec.Cmd) {}

func interruptProcess(command *exec.Cmd) error {
	return command.Process.Signal(os.Interrupt)
}
