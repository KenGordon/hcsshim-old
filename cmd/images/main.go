package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/ext4/tar2ext4"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	usernameFlag  = "username"
	passwordFlag  = "password"
	imageFlag     = "image"
	verboseFlag   = "verbose"
	outputDirFlag = "out-dir"
)

func main() {
	app := cli.NewApp()
	app.Name = "images"
	app.Usage = "tool for interacting with OCI images"
	app.Commands = []cli.Command{
		fetchUnpackCommand,
	}
	app.Flags = []cli.Flag{}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var fetchUnpackCommand = cli.Command{
	Name:  "fetch-unpacker",
	Usage: "fetches and unpacks images",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:     imageFlag + ",i",
			Usage:    "Required: container image reference",
			Required: true,
		},
		cli.StringFlag{
			Name:     outputDirFlag + ",o",
			Usage:    "Required: output directory path",
			Required: true,
		},
		cli.StringFlag{
			Name:  usernameFlag + ",u",
			Usage: "Optional: custom registry username",
		},
		cli.StringFlag{
			Name:  passwordFlag + ",p",
			Usage: "Optional: custom registry password",
		},
	},
	Action: func(cliCtx *cli.Context) error {
		image, layers, err := fetchImageLayers(cliCtx)
		if err != nil {
			return errors.Wrap(err, "failed to fetch image layers")
		}
		imageDigest, err := image.Digest()
		if err != nil {
			return errors.Wrap(err, "failed to fetch image digest")
		}

		outDir := cliCtx.String(outputDirFlag)
		if _, err := os.Stat(outDir); os.IsNotExist(err) {
			log.Debugf("creating output directory %q", outDir)
			if err := os.MkdirAll(outDir, 0755); err != nil {
				return errors.Wrapf(err, "failed to create output directory %s", outDir)
			}
		}

		readers := []io.Reader{}
		guids := []string{}

		type LayerMapping struct {
			LayerDigest    string
			PartitionGUID  string
			PartitionIndex int
		}
		type DiskInfo struct {
			DiskGuid    string
			ImageDigest string
			Layers      []LayerMapping
		}

		di := DiskInfo{
			ImageDigest: imageDigest.String(),
			Layers:      []LayerMapping{},
		}
		for layerNumber, layer := range layers {
			diffID, err := layer.DiffID()
			if err != nil {
				return errors.Wrap(err, "failed to read layer diff")
			}
			log.Debugf("Layer #%d, layer hash: %s", layerNumber, diffID.String())

			rc, err := layer.Uncompressed()
			if err != nil {
				return errors.Wrapf(err, "failed to uncompress layer %s", diffID.String())
			}
			readers = append(readers, rc)
			g, err := guid.NewV4()
			if err != nil {
				return err
			}
			guids = append(guids, g.String())

			layerDigest, err := layer.Digest()
			if err != nil {
				return errors.Wrap(err, "failed to read layer digets")
			}
			m := LayerMapping{
				LayerDigest:    layerDigest.String(),
				PartitionGUID:  g.String(),
				PartitionIndex: layerNumber,
			}
			di.Layers = append(di.Layers, m)
		}

		dg, err := guid.NewV4()
		if err != nil {
			return err
		}
		diskGuid := dg.String()

		di.DiskGuid = diskGuid

		marshelledMappinges, err := json.Marshal(di)
		if err != nil {
			return err
		}
		fmt.Printf("%v", string(marshelledMappinges))

		vhdPath := filepath.Join(cliCtx.String(outputDirFlag), diskGuid+".vhd")
		out, err := os.Create(vhdPath)
		if err != nil {
			return errors.Wrapf(err, "failed to create layer vhd %s", vhdPath)
		}

		log.Debug("converting layers to vhd")
		opts := []tar2ext4.Option{
			tar2ext4.ConvertWhiteout,
			tar2ext4.AppendVhdFooter,
		}
		if err := tar2ext4.ConvertMultiple(readers, out, guids, diskGuid, opts...); err != nil {
			return errors.Wrap(err, "failed to convert tar to ext4")
		}

		// exhaust all tar streams used
		for _, r := range readers {
			_, _ = io.Copy(ioutil.Discard, r)
		}

		return nil
	},
}

// TODO katiewasnothere: move to on top of the containerd pull/fetcher packages
func fetchImageLayers(ctx *cli.Context) (img v1.Image, layers []v1.Layer, err error) {
	image := ctx.String(imageFlag)
	ref, err := name.ParseReference(image)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to parse image reference: %s", image)
	}

	var remoteOpts []remote.Option
	if ctx.IsSet(usernameFlag) && ctx.IsSet(passwordFlag) {
		auth := authn.Basic{
			Username: ctx.String(usernameFlag),
			Password: ctx.String(passwordFlag),
		}
		authConf, err := auth.Authorization()
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to set remote")
		}
		log.Debug("using basic auth")
		authOpt := remote.WithAuth(authn.FromConfig(*authConf))
		remoteOpts = append(remoteOpts, authOpt)
	}

	img, err = remote.Image(ref, remoteOpts...)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "unable to fetch image %q, make sure it exists", image)
	}
	conf, _ := img.ConfigName()
	log.Debugf("Image id: %s", conf.String())
	layers, err = img.Layers()
	return img, layers, err
}
