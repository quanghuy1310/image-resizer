//go:build !windows
// +build !windows

package main

import "os"

// On non-Windows platforms, we don't create Job objects. These are no-ops.
type dummyJob struct{}

func createJobObject() (uintptr, error) {
	return 0, nil
}

func assignProcessToJob(job uintptr, proc *os.Process) error {
	return nil
}

func closeJob(job uintptr) {}
