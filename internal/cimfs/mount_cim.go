package cimfs

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Microsoft/go-winio/pkg/guid"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/winapi"
	"github.com/pkg/errors"
)

type MountError struct {
	Cim        string
	Op         string
	VolumeGUID guid.GUID
	Err        error
}

func (e *MountError) Error() string {
	s := "cim " + e.Op
	if e.Cim != "" {
		s += " " + e.Cim
	}
	s += " " + e.VolumeGUID.String() + ": " + e.Err.Error()
	return s
}

type cimMountInfo struct {
	cimPath  string
	refCount uint32
	volume   string
}

var mountMapping = make(map[string]*cimMountInfo)
var mountLock sync.Mutex

func MountWithFlags(cimPath string, mountFlags uint32) (string, error) {
	layerGUID, err := guid.NewV4()
	if err != nil {
		return "", &MountError{Cim: cimPath, Op: "Mount", Err: err}
	}
	if err := winapi.CimMountImage(filepath.Dir(cimPath), filepath.Base(cimPath), mountFlags, &layerGUID); err != nil {
		return "", &MountError{Cim: cimPath, Op: "Mount", VolumeGUID: layerGUID, Err: err}
	}
	return fmt.Sprintf("\\\\?\\Volume{%s}\\", layerGUID.String()), nil
}

// Mount mounts the cim at path `cimPath` and returns the mount location of that cim.
// If this cim is already mounted then nothing is done.
// This method uses the `CimMountFlagCacheRegions` mount flag when mounting the cim, if some other
// mount flag is desired use the `MountWithFlags` method.
func Mount(cimPath string) (string, error) {
	return MountWithFlags(cimPath, hcsschema.CimMountFlagCacheRegions)
}

// Unmount unmounts the cim at mounted at path `volumePath`.
func Unmount(volumePath string) error {
	// The path is expected to be in the \\?\Volume{GUID}\ format
	if volumePath[len(volumePath)-1] != '\\' {
		volumePath += "\\"
	}

	if !(strings.HasPrefix(volumePath, "\\\\?\\Volume{") && strings.HasSuffix(volumePath, "}\\")) {
		return errors.Errorf("volume path %s is not in the expected format", volumePath)
	}

	trimmedStr := strings.TrimPrefix(volumePath, "\\\\?\\Volume{")
	trimmedStr = strings.TrimSuffix(trimmedStr, "}\\")

	volGUID, err := guid.FromString(trimmedStr)
	if err != nil {
		return errors.Wrapf(err, "guid parsing failed for %s", trimmedStr)
	}

	if err := winapi.CimDismountImage(&volGUID); err != nil {
		return &MountError{VolumeGUID: volGUID, Op: "Unmount", Err: err}
	}

	return nil
}

func MountRefCounted(cimPath string) (string, error) {
	mountLock.Lock()
	defer mountLock.Unlock()

	var mountInfo *cimMountInfo
	var ok bool
	if mountInfo, ok = mountMapping[cimPath]; !ok {
		mountInfo = &cimMountInfo{
			cimPath:  cimPath,
			refCount: 0,
		}
		mountMapping[cimPath] = mountInfo

		mountedVol, err := Mount(cimPath)
		if err != nil {
			return "", err
		}
		mountInfo.volume = mountedVol
	}

	mountInfo.refCount += 1
	return mountInfo.volume, nil
}

func UnmountRefCounted(cimPath string) error {
	mountLock.Lock()
	defer mountLock.Unlock()

	var mountInfo *cimMountInfo
	var ok bool
	if mountInfo, ok = mountMapping[cimPath]; !ok {
		return nil
	}

	mountInfo.refCount -= 1
	if mountInfo.refCount == 0 {
		if err := Unmount(mountInfo.volume); err != nil {
			return err
		}
		delete(mountMapping, cimPath)
	}
	return nil
}

func GetCimMountPath(cimPath string) (string, error) {
	mountLock.Lock()
	defer mountLock.Unlock()

	var mountInfo *cimMountInfo
	var ok bool
	if mountInfo, ok = mountMapping[cimPath]; !ok {
		return "", errors.Errorf("cim %s not mounted", cimPath)
	}
	return mountInfo.volume, nil
}
