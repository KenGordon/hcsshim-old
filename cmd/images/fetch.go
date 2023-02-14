package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/ext4/gpt"
	"github.com/Microsoft/hcsshim/ext4/tar2ext4"
	"github.com/Microsoft/hcsshim/internal/memory"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	cli "github.com/urfave/cli/v2"
)

const (
	usernameFlag  = "username"
	passwordFlag  = "password"
	imageFlag     = "image"
	verboseFlag   = "verbose"
	outputDirFlag = "out-dir"
)

type ImageConfigInfo struct {
	PartitionGUID  string
	PartitionIndex int
	StartOffset    uint32
	Size           int
}
type LayerMapping struct {
	LayerDigest    string
	PartitionGUID  string
	PartitionIndex int
}
type DiskInfo struct {
	DiskGuid    string
	ImageDigest string
	Layers      []LayerMapping
	ImageConfig ImageConfigInfo
}

var fetchUnpackCommand = &cli.Command{
	Name:  "fetch-unpacker",
	Usage: "fetches and unpacks images",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     imageFlag,
			Aliases:  []string{"i"},
			Usage:    "Required: container image reference",
			Required: true,
		},
		&cli.StringFlag{
			Name:     outputFlag,
			Aliases:  []string{"o"},
			Usage:    "Required: output vhd path",
			Required: true,
		},
		&cli.StringFlag{
			Name:    usernameFlag,
			Aliases: []string{"u"},
			Usage:   "Optional: custom registry username",
		},
		&cli.StringFlag{
			Name:    passwordFlag,
			Aliases: []string{"p"},
			Usage:   "Optional: custom registry password",
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

		readers := []io.Reader{}
		guids := []guid.GUID{}

		di := DiskInfo{
			ImageDigest: imageDigest.String(),
			Layers:      []LayerMapping{},
		}

		// first layer is the image config data
		configGUID, err := guid.NewV4()
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

		for i, layer := range layers {
			layerNumber := i + 2 // +1 for config, +1 for partition indices being 1-based
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
			guids = append(guids, g)

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
		di.DiskGuid = dg.String()

		outputName := cliCtx.String(outputFlag)
		if outputName == "" {
			return errors.New("output file is required")
		}

		out, err := os.Create(outputName)
		if err != nil {
			return err
		}

		imageConfigInfo, err := createGPTDiskWithImageConfig(readers, out, configBytes, configGUID, guids, dg)
		if err != nil {
			return err
		}
		di.ImageConfig = *imageConfigInfo

		marshelledMappinges, err := json.Marshal(di)
		if err != nil {
			return err
		}
		fmt.Printf("%v", string(marshelledMappinges))

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

func createGPTDiskWithImageConfig(multipleReaders []io.Reader, w io.ReadWriteSeeker, configData []byte, configGUID guid.GUID, partitionGUIDs []guid.GUID, diskGUID guid.GUID) (*ImageConfigInfo, error) {
	// check that we have valid inputs
	if len(partitionGUIDs) != len(multipleReaders) {
		return nil, fmt.Errorf("must supply a single guid for every input file")
	}
	if len(multipleReaders) > gpt.MaxPartitions {
		return nil, fmt.Errorf("readers exceeds max number of partitions for a GPT disk: %d", len(multipleReaders))
	}

	actualSizeOfEntryArrayBytes := gpt.SizeOfPartitionEntry * (len(multipleReaders) + 1)
	sizeOfEntryArrayBytes := gpt.GetSizeOfEntryArray(len(multipleReaders) + 1)

	// find the first useable LBA and corresponding byte
	firstUseableLBA := gpt.GetFirstUseableLBA(sizeOfEntryArrayBytes)
	firstUseableByte := firstUseableLBA * gpt.BlockSizeLogical
	if _, err := w.Seek(int64(firstUseableByte), io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to the first useable LBA in disk with %v", err)
	}

	// prepare to construct the partition entries
	entries := make([]gpt.PartitionEntry, len(multipleReaders)+1) // TODO katiewasnothere: account for image config
	startLBA := firstUseableLBA
	endingLBA := firstUseableLBA

	// write the image config in the first partition

	seekStartByte := startLBA * gpt.BlockSizeLogical
	_, err := w.Seek(int64(seekStartByte), io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek file: %v", err)
	}

	imageConfigInfo := &ImageConfigInfo{
		PartitionGUID:  configGUID.String(),
		PartitionIndex: 1,
		StartOffset:    uint32(seekStartByte),
		Size:           len(configData),
	}

	if err := binary.Write(w, binary.LittleEndian, configData); err != nil {
		return nil, fmt.Errorf("failed to write backup header with: %v", err)
	}

	endingLBA = gpt.FindNextUnusedLogicalBlock(seekStartByte+uint64(len(configData))) - 1
	if endingLBA%2 == 0 {
		// make sure the next start lba is 2 aligned
		endingLBA++
	}
	entry := gpt.PartitionEntry{
		PartitionTypeGUID:   gpt.LinuxFilesystemDataGUID,
		UniquePartitionGUID: configGUID,
		StartingLBA:         startLBA,
		EndingLBA:           endingLBA, // inclusive
		Attributes:          0,
		PartitionName:       [72]byte{}, // Ignore partition name
	}
	entries[0] = entry
	startLBA = uint64(endingLBA) + 1

	// create partition entires from input readers
	for i, r := range multipleReaders {
		entryGUID := partitionGUIDs[i]

		seekStartByte := startLBA * gpt.BlockSizeLogical
		_, err := w.Seek(int64(seekStartByte), io.SeekStart)
		if err != nil {
			return nil, fmt.Errorf("failed to seek file: %v", err)
		}
		startOffset := int64(seekStartByte)
		currentConvertOpts := []tar2ext4.Option{
			tar2ext4.ConvertWhiteout,
			tar2ext4.StartWritePosition(startOffset),
		}
		bytesWritten, err := tar2ext4.ConvertTarToExt4(r, w, currentConvertOpts...)
		if err != nil {
			return nil, err
		}

		endingLBA = gpt.FindNextUnusedLogicalBlock(seekStartByte+uint64(bytesWritten)) - 1
		entry := gpt.PartitionEntry{
			PartitionTypeGUID:   gpt.LinuxFilesystemDataGUID,
			UniquePartitionGUID: entryGUID,
			StartingLBA:         startLBA,
			EndingLBA:           endingLBA, // inclusive
			Attributes:          0,
			PartitionName:       [72]byte{}, // Ignore partition name
		}
		entries[i+1] = entry // TODO katiewasnothere: this +1 accounts for image config

		// update the startLBA for the next entry
		startLBA = uint64(endingLBA) + 1
	}
	lastUseableLBA := endingLBA
	lastUsedByte := (lastUseableLBA + 1) * gpt.BlockSizeLogical // add 1 to account for bytes within the last used block

	altEntriesArrayStartLBA := gpt.FindNextUnusedLogicalBlock(uint64(lastUsedByte))
	altEntriesArrayStart := altEntriesArrayStartLBA * gpt.BlockSizeLogical

	_, err = w.Seek(int64(altEntriesArrayStart), io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek file: %v", err)
	}

	for _, e := range entries {
		if err := binary.Write(w, binary.LittleEndian, e); err != nil {
			return nil, fmt.Errorf("failed to write backup entry array with: %v", err)
		}
	}

	sizeAfterBackupEntryArrayInBytes, err := w.Seek(int64(altEntriesArrayStart+uint64(sizeOfEntryArrayBytes)), io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek file: %v", err)
	}

	alternateHeaderLBA := gpt.FindNextUnusedLogicalBlock(uint64(sizeAfterBackupEntryArrayInBytes))
	alternateHeaderInBytes := alternateHeaderLBA * gpt.BlockSizeLogical
	_, err = w.Seek(int64(alternateHeaderInBytes), io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek file: %v", err)
	}

	// only calculate the checksum for the actual partition array, do not include reserved bytes
	altEntriesCheckSum, err := gpt.CalculateChecksumPartitionEntryArray(w, uint32(altEntriesArrayStartLBA), uint32(actualSizeOfEntryArrayBytes))
	if err != nil {
		return nil, err
	}
	altGPTHeader := gpt.Header{
		Signature:                gpt.HeaderSignature,
		Revision:                 gpt.HeaderRevision,
		HeaderSize:               gpt.HeaderSize,
		HeaderCRC32:              0, // set to 0 then calculate crc32 checksum and replace
		ReservedMiddle:           0,
		MyLBA:                    alternateHeaderLBA, // LBA of this header
		AlternateLBA:             uint64(gpt.PrimaryHeaderLBA),
		FirstUsableLBA:           firstUseableLBA,
		LastUsableLBA:            lastUseableLBA,
		DiskGUID:                 diskGUID,
		PartitionEntryLBA:        altEntriesArrayStartLBA, // right before this header
		NumberOfPartitionEntries: uint32(len(multipleReaders) + 1),
		SizeOfPartitionEntry:     gpt.HeaderSizeOfPartitionEntry,
		PartitionEntryArrayCRC32: altEntriesCheckSum,
		ReservedEnd:              [420]byte{},
	}
	altGPTHeader.HeaderCRC32, err = gpt.CalculateHeaderChecksum(altGPTHeader)
	if err != nil {
		return nil, err
	}

	// write the alternate header
	if err := binary.Write(w, binary.LittleEndian, altGPTHeader); err != nil {
		return nil, fmt.Errorf("failed to write backup header with: %v", err)
	}

	// write the protectiveMBR at the beginning of the disk
	_, err = w.Seek(0, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek file: %v", err)
	}
	pMBR := gpt.ProtectiveMBR{
		BootCode:               [440]byte{},
		UniqueMBRDiskSignature: 0,
		Unknown:                0,
		PartitionRecord:        [4]gpt.PartitionMBR{},
		Signature:              gpt.ProtectiveMBRSignature,
	}

	// See gpt package for more information on the max value of the size in LBA
	sizeInLBA := uint32(alternateHeaderLBA)
	if alternateHeaderLBA >= uint64(gpt.ProtectiveMBRSizeInLBAMaxValue) {
		sizeInLBA = uint32(gpt.ProtectiveMBRSizeInLBAMaxValue)
	}

	pMBR.PartitionRecord[0] = gpt.PartitionMBR{
		BootIndicator: 0,
		StartingCHS:   gpt.ProtectiveMBRStartingCHS,
		OSType:        gpt.ProtectiveMBRTypeOS,
		EndingCHS:     gpt.CalculateEndingCHS(sizeInLBA),
		StartingLBA:   gpt.PrimaryHeaderLBA, // LBA of the GPT header
		SizeInLBA:     sizeInLBA,            // size of disk minus one is the alternate header LBA
	}

	// write the protectiveMBR
	if err := binary.Write(w, binary.LittleEndian, pMBR); err != nil {
		return nil, fmt.Errorf("failed to write backup header with: %v", err)
	}

	// write partition entries
	_, err = w.Seek(int64(gpt.PrimaryEntryArrayLBA*gpt.BlockSizeLogical), io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek file: %v", err)
	}

	for _, e := range entries {
		if err := binary.Write(w, binary.LittleEndian, e); err != nil {
			return nil, fmt.Errorf("failed to write backup entry array with: %v", err)
		}
	}

	// only calculate the checksum for the actual partition array, do not include reserved bytes if any
	entriesCheckSum, err := gpt.CalculateChecksumPartitionEntryArray(w, gpt.PrimaryEntryArrayLBA, uint32(actualSizeOfEntryArrayBytes))
	if err != nil {
		return nil, err
	}

	// write primary gpt header
	_, err = w.Seek(int64(gpt.PrimaryHeaderLBA*gpt.BlockSizeLogical), io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek file: %v", err)
	}

	hGPT := gpt.Header{
		Signature:                gpt.HeaderSignature,
		Revision:                 gpt.HeaderRevision,
		HeaderSize:               gpt.HeaderSize,
		HeaderCRC32:              0, // set to 0 then calculate crc32 checksum and replace
		ReservedMiddle:           0,
		MyLBA:                    uint64(gpt.PrimaryHeaderLBA), // LBA of this header
		AlternateLBA:             alternateHeaderLBA,
		FirstUsableLBA:           firstUseableLBA,
		LastUsableLBA:            lastUseableLBA,
		DiskGUID:                 diskGUID,
		PartitionEntryLBA:        uint64(gpt.PrimaryEntryArrayLBA), // right after this header
		NumberOfPartitionEntries: uint32(len(multipleReaders) + 1),
		SizeOfPartitionEntry:     gpt.HeaderSizeOfPartitionEntry,
		PartitionEntryArrayCRC32: entriesCheckSum,
		ReservedEnd:              [420]byte{},
	}
	hGPT.HeaderCRC32, err = gpt.CalculateHeaderChecksum(hGPT)
	if err != nil {
		return nil, err
	}

	if err := binary.Write(w, binary.LittleEndian, hGPT); err != nil {
		return nil, fmt.Errorf("failed to write backup header with: %v", err)
	}

	// align to MB
	diskSize, err := w.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if diskSize%memory.MiB != 0 {
		remainder := memory.MiB - (diskSize % memory.MiB)
		// seek the number of zeros needed to make this disk MB aligned
		diskSize, err = w.Seek(remainder, io.SeekCurrent)
		if err != nil {
			return nil, err
		}
	}

	// convert to VHD with aligned disk size
	if err := tar2ext4.ConvertToVhdWithSize(w, diskSize); err != nil {
		return nil, err
	}
	return imageConfigInfo, nil
}
