package main

import (
	"net"
	"os"

	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name: "pandora",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name: "address",
			},
		},
		Action: func(cliCtx *cli.Context) error {
			listener, err := net.Listen("tcp", cliCtx.String("address"))
			if err != nil {
				return err
			}
			grpc.Register
			return nil
		},
	}
	if err := app.Run(os.Args); err != nil {
		panic(err)
	}
}
