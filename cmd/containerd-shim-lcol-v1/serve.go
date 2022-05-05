//go:build !windows 

var serveCommand = cli.Command{
	Name: "serve",
	Hidden: true,
	SkipArgReorder: true,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name: "socket",
			Usage: "the socket path to serve",
		},
		cli.BoolFlag{
			Name: "is-sandbox",
			Usage: "is the task id a Kubernetes sandbox id",
		},
	},
	Action: func(ctx *cli.Context) error {

		// get shim options 
		shimOpts := &runhcsopts.Options{
			Debug:     false,
			DebugType: runhcsopts.Options_NPIPE,
		}

		// containerd passes the shim options protobuf via stdin.
		newShimOpts, err := readOptions(os.Stdin)
		if err != nil {
			return errors.Wrap(err, "failed to read shim options from stdin")
		} else if newShimOpts != nil {
			// We received a valid shim options struct.
			shimOpts = newShimOpts
		}

		// setup logging for shim 

		// hook up to panic.log 

		// create an event publisher for ttrpc address to containerd 

		// create new ttrpc server for hosting the task service 

		// register the services that the ttrpc server will host 

		// configure ttrpc server to listen on socket that was created during start command 

		socket := ctx.String("socket")
		if !strings.HasSuffix(socket, `.sock`) {
			return errors.New("socket is required to be a linux socket address")
		}

		// wait for the shim to exit 

		// wait for ttrpc server to be shutdown 

		return nil
	}
}