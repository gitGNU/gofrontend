// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"runtime/internal/atomic"
	_ "unsafe"
)

// For historical reasons these functions are called as though they
// were in the syscall package.
//go:linkname Cgocall syscall.Cgocall
//go:linkname CgocallDone syscall.CgocallDone
//go:linkname CgocallBack syscall.CgocallBack
//go:linkname CgocallBackDone syscall.CgocallBackDone

// A routine that may be called by SWIG.
//go:linkname _cgo_panic _cgo_panic

// iscgo is set to true if the cgo tool sets the C variable runtime_iscgo
// to true.
var iscgo bool

// cgoHasExtraM is set on startup when an extra M is created for cgo.
// The extra M must be created before any C/C++ code calls cgocallback.
var cgoHasExtraM bool

// Cgocall prepares to call from code written in Go to code written in
// C/C++. This takes the current goroutine out of the Go scheduler, as
// though it were making a system call. Otherwise the program can
// lookup if the C code blocks. The idea is to call this function,
// then immediately call the C/C++ function. After the C/C++ function
// returns, call cgocalldone. The usual Go code would look like
//     syscall.Cgocall()
//     defer syscall.Cgocalldone()
//     cfunction()
func Cgocall() {
	lockOSThread()
	mp := getg().m
	mp.ncgocall++
	mp.ncgo++
	entersyscall(0)
}

// CgocallDone prepares to return to Go code from C/C++ code.
func CgocallDone() {
	gp := getg()
	if gp == nil {
		throw("no g in CgocallDone")
	}
	gp.m.ncgo--

	// If we are invoked because the C function called _cgo_panic,
	// then _cgo_panic will already have exited syscall mode.
	if readgstatus(gp)&^_Gscan == _Gsyscall {
		exitsyscall(0)
	}

	unlockOSThread()
}

// CgocallBack is used when calling from C/C++ code into Go code.
// The usual approach is
//     syscall.CgocallBack()
//     defer syscall.CgocallBackDone()
//     gofunction()
//go:nosplit
func CgocallBack() {
	if getg() == nil || getg().m == nil {
		needm(0)
		mp := getg().m
		mp.dropextram = true
	}

	exitsyscall(0)

	if getg().m.ncgo == 0 {
		// The C call to Go came from a thread created by C.
		// The C call to Go came from a thread not currently running
		// any Go. In the case of -buildmode=c-archive or c-shared,
		// this call may be coming in before package initialization
		// is complete. Wait until it is.
		<-main_init_done
	}

	mp := getg().m
	if mp.needextram || atomic.Load(&extraMWaiters) > 0 {
		mp.needextram = false
		newextram()
	}
}

// CgocallBackDone prepares to return to C/C++ code that has called
// into Go code.
func CgocallBackDone() {
	// If we are the top level Go function called from C/C++, then
	// we need to release the m. But don't release it if we are
	// panicing; since this is the top level, we are going to
	// crash the program, and we need the g and m to print the
	// panic values.
	//
	// Dropping the m is going to clear g. This function is being
	// called as a deferred function, so we will return to
	// deferreturn which will want to clear the _defer field.
	// As soon as we call dropm another thread may call needm and
	// start using g, so we must not tamper with the _defer field
	// after dropm. So clear _defer now.
	gp := getg()
	mp := gp.m
	drop := false
	if mp.dropextram && mp.ncgo == 0 && gp._panic == nil {
		d := gp._defer
		if d == nil || d.link != nil {
			throw("unexpected g._defer in CgocallBackDone")
		}
		gp._defer = nil
		freedefer(d)
		drop = true
	}

	entersyscall(0)

	if drop {
		mp.dropextram = false
		dropm()
	}
}

// _cgo_panic may be called by SWIG code to panic.
func _cgo_panic(p *byte) {
	exitsyscall(0)
	panic(gostringnocopy(p))
}
