package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
		kernelFileVHDFooter,
	}
	app.Flags = []cli.Flag{}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var kernelFileVHDFooter = cli.Command{
	Name:  "kernel-vhd-footer",
	Usage: "adds a vhd footer to a kernel file",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:     "kernel" + ",k",
			Usage:    "Required: path to kernel file to convert",
			Required: true,
		},
	},
	Action: func(cliCtx *cli.Context) error {
		kernelPath := cliCtx.String("kernel")
		if kernelPath == "" {
			return fmt.Errorf("kernel file path not provided")
		}
		if _, err := os.Stat(kernelPath); err != nil {
			return err
		}
		kernelFile, err := os.OpenFile(kernelPath, os.O_RDWR, 0755)
		if err != nil {
			return err
		}
		return tar2ext4.ConvertToVhd(kernelFile, false)
	},
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
				PartitionIndex: layerNumber + 1, // partitions are 1 based indexed
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
			tar2ext4.AlignVHDToMB,
		}
		if err := tar2ext4.ConvertMultiple(readers, out, guids, diskGuid, opts...); err != nil {
			return errors.Wrap(err, "failed to convert tar to ext4")
		}

		// exhaust all tar streams used
		for _, r := range readers {
			_, _ = io.Copy(ioutil.Discard, r)
		}

		// TODO katiewasnothere: remove, temp for getting config file
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		configDir := filepath.Join(wd, diskGuid)
		os.MkdirAll(configDir, fs.FileMode(os.O_RDWR))
		configFileName := filepath.Join(configDir, "config.json")
		f, err := os.Create(configFileName)
		if err != nil {
			return err
		}
		config, err := image.ConfigFile()
		if err != nil {
			return err
		}
		configBytes, err := json.Marshal(config)
		if err != nil {
			return err
		}
		if _, err := f.Write(configBytes); err != nil {
			return err
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
