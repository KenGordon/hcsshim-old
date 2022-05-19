package io

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/sirupsen/logrus"
)

// NewUpstreamIO returns an UpstreamIO instance. Currently we only support sockets.
func NewUpstreamIO(ctx context.Context, id, stdout, stderr, stdin string, terminal bool, ioRetryTimeout time.Duration) (UpstreamIO, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"stdin":    stdin,
		"stdout":   stdout,
		"stderr":   stderr,
		"terminal": terminal,
	}).Debug("NewNpipeIO")

	return newPipeIO(ctx, stdin, stdout, stderr, terminal, ioRetryTimeout)
}

func newPipeIO(ctx context.Context, stdin, stdout, stderr string, terminal bool, retryTimeout time.Duration) (*pipeIO, error) {
	// TODO katiewasnothere: how to support terminal
	var (
		pipes                             []*pipe
		stdinPipe, stdoutPipe, stderrPipe *pipe
		err                               error
	)
	// cleanup in case of an error
	defer func() {
		if err != nil {
			for _, p := range pipes {
				p.Close()
			}
		}
	}()
	if stdin != "" {
		if stdinPipe, err = newPipe(); err != nil {
			return nil, err
		}
		pipes = append(pipes, stdinPipe)
		// TODO katiewasnothere: permissions
		/*if err = unix.Fchown(int(stdin.r.Fd()), uid, gid); err != nil {
			return nil, errors.Wrap(err, "failed to chown stdin")
		}*/
	}
	if stdout != "" {
		if stdoutPipe, err = newPipe(); err != nil {
			return nil, err
		}
		pipes = append(pipes, stdoutPipe)
		// TODO katiewasnothere: permissions for io on init?
		/*if err = unix.Fchown(int(stdout.w.Fd()), uid, gid); err != nil {
			return nil, errors.Wrap(err, "failed to chown stdout")
		}*/
	}
	if stderr != "" {
		if stderrPipe, err = newPipe(); err != nil {
			return nil, err
		}
		pipes = append(pipes, stderrPipe)
		/*if err = unix.Fchown(int(stderr.w.Fd()), uid, gid); err != nil {
			return nil, errors.Wrap(err, "failed to chown stderr")
		}*/
	}
	return &pipeIO{
		in:  stdinPipe,
		out: stdoutPipe,
		err: stderrPipe,
	}, nil
}

// TODO katiewasnothere: took this from go-runc
type pipe struct {
	r *os.File
	w *os.File
}

func newPipe() (*pipe, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	return &pipe{
		r: r,
		w: w,
	}, nil
}

func (p *pipe) Close() error {
	err := p.w.Close()
	if rerr := p.r.Close(); err == nil {
		err = rerr
	}
	return err
}

type pipeIO struct {
	in  *pipe
	out *pipe
	err *pipe
}

var _ = (UpstreamIO)(&pipeIO{})

func (i *pipeIO) Stdin() io.Reader {
	if i.in == nil {
		return nil
	}
	return i.in.r
}

func (i *pipeIO) StdinPath() string {
	if i.in == nil {
		return ""
	}
	return i.in.r.Name()
}

func (i *pipeIO) Stdout() io.Writer {
	if i.out == nil {
		return nil
	}
	return i.out.w
}

func (i *pipeIO) StdoutPath() string {
	if i.out == nil {
		return ""
	}
	return i.out.w.Name()
}

func (i *pipeIO) Stderr() io.Writer {
	if i.err == nil {
		return nil
	}
	return i.err.w
}

func (i *pipeIO) StderrPath() string {
	if i.err == nil {
		return ""
	}
	return i.err.w.Name()
}

func (i *pipeIO) Terminal() bool {
	// TODO katiewasnothere: for now just putting this as always false
	return false
}

func (i *pipeIO) Close(ctx context.Context) {
	for _, v := range []*pipe{
		i.in,
		i.out,
		i.err,
	} {
		if v != nil {
			if cerr := v.Close(); cerr != nil {
				log.G(ctx).WithError(cerr).Error("error while closing pipe")
			}
		}
	}
}

func (i *pipeIO) CloseStdin(ctx context.Context) {
	if i.in != nil {
		if err := i.in.Close(); err != nil {
			log.G(ctx).WithError(err).Error("error while closing stdin pipe")
		}
	}
}
