package hvlitevm

import (
	"os"
	"path/filepath"

	"github.com/opencontainers/runtime-spec/specs-go"
)

type Options struct {
	ID    string
	Owner string

	// BootFilesPath specifies the location to find boot files and binaries
	BootFilesPath string
	// KernelFile specifies the name of the kernel image to boot from
	KernelFile string
	// InitrdPath specifies the name of the initrd to use as the rootfs
	InitrdPath string
	// BinPath is the name of the binary implementing the vmservice interface
	BinPath string

	TTRPCAddress string

	// OCISpec is the spec that the remote VM should be created with
	OCISpec *specs.Spec
}

func NewDefaultOptions(id, owner string) *Options {
	return &Options{
		ID:            id,
		Owner:         owner,
		BootFilesPath: filepath.Join(filepath.Dir(os.Args[0]), "LinuxBootFiles"),
		KernelFile:    "vmlinux",
		InitrdPath:    "initrd.img",
		BinPath:       filepath.Join(filepath.Dir(os.Args[0]), "hvlite"),
	}
}
