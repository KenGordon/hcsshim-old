package main

import "net"

// This file implements Reader and Writer for windows named pipes. It was
// designed to be used with Cmd stdio and to enable exec and attach.

// PipeWriter is a writer for a windows named pipe. It implements io.Writer
//
// If the pipe is nil, Write will succeed and do nothing.
// If the pipe is not nil, Write will write to the pipe.
type PipeWriter struct {
	pipe *net.Conn
}

// PipeReader is a reader for a windows named pipe. It implements io.Reader
//
// If the pipe is nil, Read will succeed and do nothing.
// If the pipe is not nil, Read will read from the pipe.
type PipeReader struct {
	pipe *net.Conn
}

func (p *PipeWriter) Write(b []byte) (n int, err error) {
	if p.pipe == nil {
		return len(b), nil
	}
	return (*p.pipe).Write(b)
}

func (p *PipeReader) Read(b []byte) (n int, err error) {
	if p.pipe == nil {
		return 0, nil
	}
	return (*p.pipe).Read(b)
}
