package sdjournalwarp

/*
#cgo LDFLAGS: -ldl
#include <stdlib.h>
#include <dlfcn.h>
#include <systemd/sd-journal.h>

int my_sd_journal_open_files_fd(void *f, sd_journal **ret, int fds[], unsigned n_fds, int flags) {
	int (*sd_journal_open_files_fd_ptr)(sd_journal **, int[], unsigned, int) = f;
	return sd_journal_open_files_fd_ptr(ret, fds, n_fds, flags);
}

void sdjournalwarp_sd_journal_close(void *f, sd_journal *j) {
	void (*sd_journal_close_ptr)(sd_journal *) = f;
	sd_journal_close_ptr(j);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"syscall"
	"unsafe"

	"github.com/coreos/go-systemd/v22/sdjournal"
)

var ErrSoNotFound = errors.New("unable to open a handle to the library")

type LibHandle struct {
	Handle  unsafe.Pointer
	Libname string
}

func GetHandle(libs []string) (*LibHandle, error) {
	for _, name := range libs {
		libname := C.CString(name)
		defer C.free(unsafe.Pointer(libname))
		handle := C.dlopen(libname, C.RTLD_LAZY)
		if handle != nil {
			h := &LibHandle{
				Handle:  handle,
				Libname: name,
			}
			return h, nil
		}
	}
	return nil, ErrSoNotFound
}

func (l *LibHandle) GetSymbolPointer(symbol string) (unsafe.Pointer, error) {
	sym := C.CString(symbol)
	defer C.free(unsafe.Pointer(sym))

	C.dlerror()
	p := C.dlsym(l.Handle, sym)
	e := C.dlerror()
	if e != nil {
		return nil, fmt.Errorf("error resolving symbol %q: %v", symbol, errors.New(C.GoString(e)))
	}

	return p, nil
}

func (l *LibHandle) Close() error {
	C.dlerror()
	C.dlclose(l.Handle)
	e := C.dlerror()
	if e != nil {
		return fmt.Errorf("error closing %v: %v", l.Libname, errors.New(C.GoString(e)))
	}

	return nil
}

type Journal struct {
	sdjournal.Journal
}

func NewJournalFromHandle(fd []uintptr) (j *Journal, err error) {
	j = &Journal{}

	sd_journal_open_files_fd, err := getFunction("sd_journal_open_files_fd")
	if err != nil {
		return nil, err
	}

	sd_journal_close, err := getFunction("sd_journal_close")
	if err != nil {
		return nil, err
	}

	var validFds []C.int
	for _, f := range fd {
		var cj *C.sd_journal
		dupFd, err := syscall.Dup(int(f))
		if err != nil {
			slog.Warn("Failed to duplicate fd", "fd", f, "error", err)
			continue
		}
		oneFd := C.int(dupFd)
		r := C.my_sd_journal_open_files_fd(sd_journal_open_files_fd, &cj, &oneFd, 1, 0)
		if r >= 0 {
			C.sdjournalwarp_sd_journal_close(sd_journal_close, cj)
			validFds = append(validFds, C.int(f)) // Use the ORIGINAL fd for the final array
		} else {
			slog.Warn("Skipping corrupted journal fd", "fd", f, "error", syscall.Errno(-r))
			syscall.Close(dupFd) // We must manually close dupFd if open failed
		}
	}

	if len(validFds) == 0 {
		return nil, fmt.Errorf("failed to open journals in fds: no valid journal files found")
	}

	var cj *C.sd_journal
	r := C.my_sd_journal_open_files_fd(sd_journal_open_files_fd, &cj, &validFds[0], C.unsigned(len(validFds)), 0)
	if r < 0 {
		return nil, fmt.Errorf("failed to open journals in fds: %s", syscall.Errno(-r).Error())
	}

	// Overwrite the embedded Journal with the pointer to cjournal
	// The internal representation of sdjournal.Journal is:
	// type Journal struct { cjournal *C.sd_journal; mu sync.Mutex }
	// We need to write *C.sd_journal to the first field of sdjournal.Journal.
	p := unsafe.Pointer(&j.Journal)
	*(*unsafe.Pointer)(p) = unsafe.Pointer(cj)

	return j, nil
}

var (
	// lazy initialized
	libsystemdHandle *LibHandle

	libsystemdMutex     = &sync.Mutex{}
	libsystemdFunctions = map[string]unsafe.Pointer{}
	libsystemdNames     = []string{
		// systemd < 209
		"libsystemd-journal.so.0",
		"libsystemd-journal.so",

		// systemd >= 209 merged libsystemd-journal into libsystemd proper
		"libsystemd.so.0",
		"libsystemd.so",
	}
)

func getFunction(name string) (unsafe.Pointer, error) {
	libsystemdMutex.Lock()
	defer libsystemdMutex.Unlock()

	if libsystemdHandle == nil {
		h, err := GetHandle(libsystemdNames)
		if err != nil {
			return nil, err
		}

		libsystemdHandle = h
	}

	f, ok := libsystemdFunctions[name]
	if !ok {
		var err error
		f, err = libsystemdHandle.GetSymbolPointer(name)
		if err != nil {
			return nil, err
		}

		libsystemdFunctions[name] = f
	}

	return f, nil
}
