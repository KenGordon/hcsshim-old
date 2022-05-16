package io

// NewUpstreamIO returns an UpstreamIO instance. Currently we only support sockets.
func NewUpstreamIO(ctx context.Context, id, stdout, stderr, stdin string, terminal bool, ioRetryTimeout time.Duration) (UpstreamIO, error) {

}

func NewPipeIO(ctx context.Context, stdin, stdout, stderr string, terminal bool, retryTimeout time.Duration) (*pipeIO, error) {
	// TODO katiewasnothere: how to support terminal
	var (
		pipes                 []*pipe
		stdin, stdout, stderr *pipe
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
		if stdin, err = newPipe(); err != nil {
			return nil, err
		}
		pipes = append(pipes, stdin)
		// TODO katiewasnothere: permissions
		/*if err = unix.Fchown(int(stdin.r.Fd()), uid, gid); err != nil {
			return nil, errors.Wrap(err, "failed to chown stdin")
		}*/
	}
	if stdout != "" {
		if stdout, err = newPipe(); err != nil {
			return nil, err
		}
		pipes = append(pipes, stdout)
		// TODO katiewasnothere: permissions for io on init?
		/*if err = unix.Fchown(int(stdout.w.Fd()), uid, gid); err != nil {
			return nil, errors.Wrap(err, "failed to chown stdout")
		}*/
	}
	if stderr != "" {
		if stderr, err = newPipe(); err != nil {
			return nil, err
		}
		pipes = append(pipes, stderr)
		/*if err = unix.Fchown(int(stderr.w.Fd()), uid, gid); err != nil {
			return nil, errors.Wrap(err, "failed to chown stderr")
		}*/
	}
	return &pipeIO{
		in:  stdin,
		out: stdout,
		err: stderr,
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

var _ = (UpstreamIO)(&PipeIO{})

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

func (i *pipeIO) Close(ctx context.Context) error {
	var err error
	for _, v := range []*pipe{
		i.in,
		i.out,
		i.err,
	} {
		if v != nil {
			if cerr := v.Close(); err == nil {
				err = cerr
			}
		}
	}
	return err
}

func (i *pipeIO) CloseStdin(ctx context.Context) error {
	if i.in == nil {
		return nil
	}
	return i.in.Close()
}
