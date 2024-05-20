package verity

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/Microsoft/hcsshim/ext4/dmverity"
	"github.com/Microsoft/hcsshim/ext4/tar2ext4"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// fileSystemSize retrieves ext4 fs SuperBlock and returns the file system size and block size
func fileSystemSize(ctx context.Context, vhdPath string) (int64, int, error) {

	log.G(ctx).WithField("vhdPath", vhdPath).Debug("fileSystemSize")
	vhd, err := os.Open(vhdPath)
	if err != nil {
		log.G(ctx).WithField("vhdPath", vhdPath).Debugf("fileSystemSize %s", err.Error())
		return 0, 0, fmt.Errorf("failed to open VHD file: %w", err)
	}
	defer vhd.Close()

	return tar2ext4.Ext4FileSystemSize(vhd)
}

func logLS(ctx context.Context, desc string, path string) {
	command := exec.Command("/bin/ls", "-l", path)
	if result, err := command.Output(); err != nil {
		log.G(ctx).WithFields(logrus.Fields{
			"error":   err,
			"command": command.Args,
		}).Debugf("ls -l %s command returns error", path)
	} else {
		log.G(ctx).WithFields(logrus.Fields{"result": result, "desc": desc}).Debugf("ls -l %s command returns result", path)
	}

}

func logSysBusScsi(ctx context.Context, desc string) {
	logLS(ctx, desc, "/sys/bus/scsi/devices")
	logLS(ctx, desc, "/sys/bus/scsi/devices/0:0:0:2/block") // typically we are missing sdc here.
}

// ReadVeritySuperBlock reads ext4 super block for a given VHD to then further read the dm-verity super block
// and root hash
func ReadVeritySuperBlock(ctx context.Context, layerPath string) (*guestresource.DeviceVerityInfo, error) {
	// dm-verity information is expected to be appended, the size of ext4 data will be the offset
	// of the dm-verity super block, followed by merkle hash tree

	logSysBusScsi(ctx, "ReadVeritySuperBlock top")

	ext4SizeInBytes, ext4BlockSize, err := fileSystemSize(ctx, layerPath)
	if err != nil {
		logSysBusScsi(ctx, "ReadVeritySuperBlock fail 1")
		log.G(ctx).WithField("layerPath", layerPath).Debugf("ReadVeritySuperBlock - fileSystemSize %s", err.Error())
		return nil, err
	}

	dmvsb, err := dmverity.ReadDMVerityInfo(layerPath, ext4SizeInBytes)
	if err != nil {
		logSysBusScsi(ctx, "ReadVeritySuperBlock fail 2")
		log.G(ctx).WithField("layerPath", layerPath).Debugf("ReadVeritySuperBlock - ReadDMVerityInfo %s", err.Error())
		return nil, errors.Wrap(err, "failed to read dm-verity super block")
	}
	log.G(ctx).WithFields(logrus.Fields{
		"layerPath":     layerPath,
		"rootHash":      dmvsb.RootDigest,
		"algorithm":     dmvsb.Algorithm,
		"salt":          dmvsb.Salt,
		"dataBlocks":    dmvsb.DataBlocks,
		"dataBlockSize": dmvsb.DataBlockSize,
	}).Debug("dm-verity information")

	return &guestresource.DeviceVerityInfo{
		Ext4SizeInBytes: ext4SizeInBytes,
		BlockSize:       ext4BlockSize,
		RootDigest:      dmvsb.RootDigest,
		Algorithm:       dmvsb.Algorithm,
		Salt:            dmvsb.Salt,
		Version:         int(dmvsb.Version),
		SuperBlock:      true,
	}, nil
}
