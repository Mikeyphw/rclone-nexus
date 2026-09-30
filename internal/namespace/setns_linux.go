//go:build linux

package namespace

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
)

const cloneNewNS = 0x00020000

func setns(fd uintptr) error {
	_, _, errno := syscall.RawSyscall(sysSetns, fd, cloneNewNS, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// withMountNamespace isolates setns on a dedicated locked OS thread. If the
// original namespace cannot be restored, the goroutine exits while the thread
// is still locked; the Go runtime then discards that OS thread instead of ever
// returning a foreign-namespace thread to the scheduler pool.
func withMountNamespace(pid int, expectedID string, fn func() error) error {
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		current, err := os.Open("/proc/self/ns/mnt")
		if err != nil {
			runtime.UnlockOSThread()
			result <- err
			return
		}
		defer current.Close()

		targetPath := fmt.Sprintf("/proc/%d/ns/mnt", pid)
		actualID, err := os.Readlink(targetPath)
		if err != nil {
			runtime.UnlockOSThread()
			result <- err
			return
		}
		if expectedID != "" && actualID != expectedID {
			runtime.UnlockOSThread()
			result <- fmt.Errorf("target mount namespace identity changed")
			return
		}
		target, err := os.Open(targetPath)
		if err != nil {
			runtime.UnlockOSThread()
			result <- err
			return
		}
		defer target.Close()
		if err := setns(target.Fd()); err != nil {
			runtime.UnlockOSThread()
			result <- fmt.Errorf("enter mount namespace: %w", err)
			return
		}

		actionErr := fn()
		if restoreErr := setns(current.Fd()); restoreErr != nil {
			result <- fmt.Errorf("restore mount namespace: %w", restoreErr)
			// Do not unlock. Exiting a goroutine that remains locked to its OS
			// thread causes that contaminated thread to be terminated.
			runtime.Goexit()
		}
		runtime.UnlockOSThread()
		result <- actionErr
	}()
	return <-result
}
