/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package kmsg exposes the Linux structured kernel log (/dev/kmsg) as
// an io.ReadCloser that delivers one record per Read.
package kmsg

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

var KmsgPath = "/dev/kmsg"

// MaxRecordSize is CONSOLE_EXT_LOG_MAX from kernel/printk/printk.c —
// the kernel never emits a single /dev/kmsg record longer than this.
// Pass a buffer at least this large to Reader.Read so the kernel
// never returns EINVAL for an oversized record.
const MaxRecordSize = 8 * 1024

// Reader streams /dev/kmsg records via io.Reader. Each successful Read
// returns the bytes of one record verbatim, including the wire-format
// header (<pri>,<seq>,<ts>,<flags>;) and the trailing record-terminator
// newline.
//
// Read does not recover from kernel errors: EPIPE (ring-buffer
// overflow), EINVAL (oversized record), and any other Read failure
// are returned to the caller as-is. The next Read after such an error
// reseeks to /dev/kmsg's current tail before issuing the underlying
// read; records produced during the error window are unrecoverable.
//
// On the poller-backed fd, Read blocks until a record arrives or the
// underlying file is closed (returning os.ErrClosed). Callers that
// want to unblock Read on context cancel must arrange a goroutine
// that calls Close — closing from anywhere else races with the
// parked Read.
type Reader struct {
	kmsg    io.ReadSeekCloser
	reading bool
}

// Open opens /dev/kmsg in non-blocking, close-on-exec mode. The
// initial seek to the log's tail is deferred to the first Read.
//
// O_NONBLOCK matters: os.NewFile inspects the fd and, if it is
// non-blocking, registers it with the runtime poller (see kindNonBlock
// in src/os/file_unix.go). Without it the read would park an OS thread
// in a blocking syscall instead of just a goroutine on epoll_wait.
//
// O_CLOEXEC keeps the fd out of any child processes spawned later.
func Open() (*Reader, error) {
	fd, err := syscall.Open(KmsgPath, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", KmsgPath, err)
	}
	return &Reader{kmsg: os.NewFile(uintptr(fd), KmsgPath)}, nil
}

func (r *Reader) Read(p []byte) (int, error) {
	if !r.reading {
		if _, err := r.kmsg.Seek(0, io.SeekEnd); err != nil {
			return 0, fmt.Errorf("seeking to end of %s: %w", KmsgPath, err)
		}
		r.reading = true
	}
	n, err := r.kmsg.Read(p)
	if err != nil {
		r.reading = false
		err = fmt.Errorf("reading %s: %w", KmsgPath, err)
	}
	return n, err
}

func (r *Reader) Close() error {
	return r.kmsg.Close()
}
