package hvlitevm

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"

	"github.com/pkg/errors"
)

const (
	entropyListenerPort   = 1
	logOutputListenerPort = 109 // TODO katiewasnothere: 129 as port here doesn't work
)

func outputHandler(r io.Reader) {
	_, _ = io.Copy(os.Stderr, r)
}

func listenHybridVsock(udsPath string, port uint32) (net.Listener, error) {
	return net.Listen("unix", fmt.Sprintf("%s_%d", udsPath, port))
}

// Get a random unix socket address to use. The "randomness" equates to makes a temp file to reserve a name
// and then shortly after deleting it and using this as the socket address.
func randomUnixSockAddr() (string, error) {
	// Make a temp file and delete to "reserve" a unique name for the unix socket
	f, err := ioutil.TempFile("", "")
	if err != nil {
		return "", errors.Wrap(err, "failed to create temp file for unix socket")
	}

	if err := f.Close(); err != nil {
		return "", errors.Wrap(err, "failed to close temp file")
	}

	if err := os.Remove(f.Name()); err != nil {
		return "", errors.Wrap(err, "failed to delete temp file to free up name")
	}

	return f.Name(), nil
}

// acceptAndClose accepts a connection and then closes a listener. If the
// context becomes done or the utility VM terminates, the operation will be
// cancelled (but the listener will still be closed).
func acceptAndClose(ctx context.Context, l net.Listener) (net.Conn, error) {
	var conn net.Conn
	ch := make(chan error)
	go func() {
		var err error
		conn, err = l.Accept()
		ch <- err
	}()
	select {
	case err := <-ch:
		l.Close()
		return conn, err
	case <-ctx.Done():
	}
	l.Close()
	err := <-ch
	if err == nil {
		return conn, err
	}
	// Prefer context error to VM error to accept error in order to return the
	// most useful error.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, err
}
