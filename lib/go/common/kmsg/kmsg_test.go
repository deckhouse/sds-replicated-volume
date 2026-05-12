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

package kmsg

import (
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// recordReader returns one canned record per Read. errAt injects a
// one-shot error at that record index; -1 disables.
type recordReader struct {
	records   [][]byte
	idx       int
	errAt     int
	err       error
	seekCalls int
	seekErr   error
}

func (r *recordReader) Read(p []byte) (int, error) {
	if r.idx == r.errAt {
		r.errAt = -1
		return 0, r.err
	}
	if r.idx >= len(r.records) {
		// Mirrors what a real fd does after Close, so tests reuse
		// the production stop path instead of needing a test-only
		// io.EOF branch.
		return 0, os.ErrClosed
	}
	n := copy(p, r.records[r.idx])
	r.idx++
	return n, nil
}

func (r *recordReader) Seek(offset int64, _ int) (int64, error) {
	r.seekCalls++
	return offset, r.seekErr
}

func (*recordReader) Close() error { return nil }

// readAll collects records via Reader.Read until it returns
// os.ErrClosed; any other error fails the test.
func readAll(t *testing.T, r *Reader) []string {
	t.Helper()
	var got []string
	buf := make([]byte, MaxRecordSize)
	for {
		n, err := r.Read(buf)
		if err != nil {
			if !errors.Is(err, os.ErrClosed) {
				t.Fatalf("Read error: %v", err)
			}
			return got
		}
		got = append(got, string(buf[:n]))
	}
}

func TestReaderHappyPath(t *testing.T) {
	t.Parallel()
	src := &recordReader{
		records: [][]byte{
			[]byte("6,1,1000,-;first\n"),
			[]byte("5,2,2000,-;second\n"),
			[]byte("4,3,3000,-;third\n"),
		},
		errAt: -1,
	}
	got := readAll(t, &Reader{kmsg: src})
	want := []string{"6,1,1000,-;first\n", "5,2,2000,-;second\n", "4,3,3000,-;third\n"}
	if len(got) != len(want) {
		t.Fatalf("want %d records, got %d (%v)", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i], w)
		}
	}
	if src.seekCalls != 1 {
		t.Errorf("seekCalls: got %d, want 1 (single seek-to-tail on first Read)", src.seekCalls)
	}
}

func TestReaderResumesAfterError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("transient")
	src := &recordReader{
		records: [][]byte{
			[]byte("6,1,1000,-;before\n"),
			[]byte("6,2,2000,-;after\n"),
		},
		errAt: 1,
		err:   wantErr,
	}
	r := &Reader{kmsg: src}
	buf := make([]byte, MaxRecordSize)

	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("Read 1 error: %v", err)
	}
	if got := string(buf[:n]); got != "6,1,1000,-;before\n" {
		t.Errorf("Read 1 = %q, want %q", got, "6,1,1000,-;before\n")
	}
	if src.seekCalls != 1 {
		t.Errorf("after Read 1: seekCalls = %d, want 1", src.seekCalls)
	}

	if _, err := r.Read(buf); !errors.Is(err, wantErr) {
		t.Fatalf("Read 2: got err=%v, want %v", err, wantErr)
	}
	if src.seekCalls != 1 {
		t.Errorf("after Read 2 (error): seekCalls = %d, want still 1", src.seekCalls)
	}

	n, err = r.Read(buf)
	if err != nil {
		t.Fatalf("Read 3 error: %v", err)
	}
	if got := string(buf[:n]); got != "6,2,2000,-;after\n" {
		t.Errorf("Read 3 = %q, want %q", got, "6,2,2000,-;after\n")
	}
	if src.seekCalls != 2 {
		t.Errorf("after Read 3 (post-error reseek): seekCalls = %d, want 2", src.seekCalls)
	}
}

func TestReaderReseekErrorPropagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("seek broken")
	src := &recordReader{
		records: [][]byte{[]byte("6,1,1000,-;hi\n")},
		errAt:   -1,
		seekErr: wantErr,
	}
	buf := make([]byte, MaxRecordSize)
	_, err := (&Reader{kmsg: src}).Read(buf)
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err=%v, want it to wrap %v", err, wantErr)
	}
}

func TestReaderUnblocksOnClose(t *testing.T) {
	t.Parallel()
	r := &Reader{kmsg: newBlockingReader()}

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, MaxRecordSize)
		_, err := r.Read(buf)
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	_ = r.Close()

	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("Read returned err=%v, want os.ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not return after Close")
	}
}

// blockingReader parks Read until Close is invoked, mimicking the real
// /dev/kmsg behavior under the poller: a Read on an empty buffer waits,
// and closing the fd unparks it with os.ErrClosed.
type blockingReader struct {
	ch     chan struct{}
	closed atomic.Bool
}

func newBlockingReader() *blockingReader {
	return &blockingReader{ch: make(chan struct{})}
}

func (b *blockingReader) Read(_ []byte) (int, error) {
	<-b.ch
	return 0, os.ErrClosed
}

func (*blockingReader) Seek(int64, int) (int64, error) { return 0, nil }

func (b *blockingReader) Close() error {
	if b.closed.CompareAndSwap(false, true) {
		close(b.ch)
	}
	return nil
}
