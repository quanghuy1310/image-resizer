//go:build windows
// +build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createJobObject creates a Job object with KILL_ON_JOB_CLOSE set so that
// when the job handle is closed (for example when parent exits), all child
// processes assigned to the job are terminated by the OS.
func createJobObject() (uintptr, error) {
	name := syscall.StringToUTF16Ptr("")
	h, err := windows.CreateJobObject(nil, name)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObject failed: %w", err)
	}

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	size := uint32(unsafe.Sizeof(info))
	// SetInformationJobObject
	_, err = windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), size)
	if err != nil {
		windows.CloseHandle(h)
		return 0, fmt.Errorf("SetInformationJobObject failed: %w", err)
	}
	return uintptr(h), nil
}

// assignProcessToJob assigns an already-started process to the given job object.
func assignProcessToJob(job uintptr, proc *os.Process) error {
	if job == 0 || proc == nil {
		return nil
	}
	// Open process handle
	pHandle, err := windows.OpenProcess(windows.PROCESS_ALL_ACCESS, false, uint32(proc.Pid))
	if err != nil {
		return fmt.Errorf("OpenProcess failed: %w", err)
	}
	defer windows.CloseHandle(pHandle)
	if err := windows.AssignProcessToJobObject(windows.Handle(job), pHandle); err != nil {
		return fmt.Errorf("AssignProcessToJobObject failed: %w", err)
	}
	return nil
}

func closeJob(job uintptr) {
	if job == 0 {
		return
	}
	windows.CloseHandle(windows.Handle(job))
}
