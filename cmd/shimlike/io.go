package main

import (
	"errors"
	"io"
	"net"
)

// This file implements Reader and Writer for windows named pipes. It was
// designed to be used with Cmd stdio and to enable exec and attach.

// PipeWriter is a writer for a windows named pipe. It implements io.Writer
//
// If the pipe is nil, Write will succeed and do nothing.
// If the pipe is not nil, Write will write to the pipe.
//
// If the pipe becomes closed, Write will succeed, and future calls will do nothing until the pipe is set again.
type PipeWriter struct {
	pipe *net.Conn
}

// PipeReader is a reader for a windows named pipe. It implements io.Reader
//
// If the pipe is nil, Read will succeed and do nothing.
// If the pipe is not nil, Read will read from the pipe.
//
// If the pipe becomes closed or EOF, Read will succeed, and future calls will do nothing until the pipe is set again.
type PipeReader struct {
	pipe *net.Conn
}

func (p *PipeWriter) Write(b []byte) (n int, err error) {
	if p.pipe == nil {
		return len(b), nil
	}
	written, err := (*p.pipe).Write(b)

	// If the pipe is closed, handle it gracefully and set the pipe to nil
	if errors.Is(err, io.ErrClosedPipe) {
		p.pipe = nil
		return written, nil
	}
	if err != nil {
		return written, err
	}
	return written, nil
}

func (p *PipeReader) Read(b []byte) (n int, err error) {
	if p.pipe == nil {
		return 0, nil
	}
	read, err := (*p.pipe).Read(b)

	// If the pipe is closed or EOF, handle it gracefully and set the pipe to nil
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
		p.pipe = nil
		return read, nil
	}
	if err != nil {
		return read, err
	}
	return read, nil
}
