package io

import (
	"context"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/containerd/fifo"
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
		pipes                             []io.ReadWriteCloser
		stdinFifo, stdoutFifo, stderrFifo io.ReadWriteCloser
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
		stdinFifo, err = fifo.OpenFifo(ctx, stdin, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		log.G(ctx).WithField("stdin", stdin).Infof("the fifo for stdin is %v", stdinFifo)
		pipes = append(pipes, stdinFifo)
		// TODO katiewasnothere: permissions
		/*if err = unix.Fchown(int(stdin.r.Fd()), uid, gid); err != nil {
			return nil, errors.Wrap(err, "failed to chown stdin")
		}*/
	}
	if stdout != "" {
		stdoutFifo, err = fifo.OpenFifo(ctx, stdout, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		log.G(ctx).WithField("stdout", stdout).Infof("the fifo for stdout is %v", stdoutFifo)

		pipes = append(pipes, stdoutFifo)
		// TODO katiewasnothere: permissions for io on init?
		/*if err = unix.Fchown(int(stdout.w.Fd()), uid, gid); err != nil {
			return nil, errors.Wrap(err, "failed to chown stdout")
		}*/
	}
	if stderr != "" {
		stderrFifo, err = fifo.OpenFifo(ctx, stderr, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		log.G(ctx).WithField("stderr", stderr).Infof("the fifo for stderr is %v", stderrFifo)

		pipes = append(pipes, stderrFifo)
		/*if err = unix.Fchown(int(stderr.w.Fd()), uid, gid); err != nil {
			return nil, errors.Wrap(err, "failed to chown stderr")
		}*/
	}
	return &pipeIO{
		in:       stdinFifo,
		inName:   stdin,
		out:      stdoutFifo,
		outName:  stdout,
		err:      stderrFifo,
		errName:  stderr,
		terminal: terminal,
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
	in       io.ReadWriteCloser
	inName   string
	out      io.ReadWriteCloser
	outName  string
	err      io.ReadWriteCloser
	errName  string
	terminal bool
}

var _ = (UpstreamIO)(&pipeIO{})

func (i *pipeIO) Stdin() io.Reader {
	if i.in == nil {
		return nil
	}
	return i.in
}

func (i *pipeIO) StdinPath() string {
	if i.in == nil {
		return ""
	}
	return i.inName
}

func (i *pipeIO) Stdout() io.Writer {
	if i.out == nil {
		return nil
	}
	return i.out
}

func (i *pipeIO) StdoutPath() string {
	if i.out == nil {
		return ""
	}
	return i.outName
}

func (i *pipeIO) Stderr() io.Writer {
	if i.err == nil {
		return nil
	}
	return i.err
}

func (i *pipeIO) StderrPath() string {
	if i.err == nil {
		return ""
	}
	return i.errName
}

func (i *pipeIO) Terminal() bool {
	return i.terminal
}

func (i *pipeIO) Close(ctx context.Context) {
	for _, v := range []io.ReadWriteCloser{
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
