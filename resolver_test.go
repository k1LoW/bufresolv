package bsrr

import (
	"testing"
)

func TestBufLock(t *testing.T) {
	b, err := New(BufLock("testdata/buf.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if want := 6; len(b.fds) != want {
		t.Errorf("got %d, want %d", len(b.fds), want)
	}
}

func TestModule(t *testing.T) {
	b, err := New(Module("buf.build/bufbuild/protovalidate/tree/b983156c5e994cc9892e0ce3e64e17e0"))
	if err != nil {
		t.Fatal(err)
	}
	if want := 6; len(b.fds) != want {
		t.Errorf("got %d, want %d", len(b.fds), want)
	}
}
