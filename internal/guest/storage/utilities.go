//go:build linux
// +build linux

package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/pkg/errors"
)

// The following constants aren't defined in the io or os libraries.
//
//nolint:stylecheck // ST1003: ALL_CAPS
const (
	SEEK_DATA = 3
	SEEK_HOLE = 4
)

// export this variable so it can be mocked to aid in testing for consuming packages
var filepathglob = filepath.Glob

// WaitForFileMatchingPattern waits for a single file that matches the given path pattern and returns the full path
// to the resulting file
func WaitForFileMatchingPattern(ctx context.Context, pattern string) (string, error) {
	for {
		files, err := filepathglob(pattern)
		if err != nil {
			return "", err
		}
		if len(files) == 0 {
			select {
			case <-ctx.Done():
				return "", errors.Wrapf(ctx.Err(), "timed out waiting for file matching pattern %s to exist", pattern)
			default:
				time.Sleep(time.Millisecond * 10)
				continue
			}
		} else if len(files) > 1 {
			return "", fmt.Errorf("more than one file could exist for pattern \"%s\"", pattern)
		}
		return files[0], nil
	}
}

// CreateSparseEmptyFile creates a sparse file of the specified size. The whole
// file is empty, so the size on disk is zero, only the logical size is the
// specified one.
func CreateSparseEmptyFile(ctx context.Context, path string, size int64) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return errors.Wrapf(err, "failed to create: %s", path)
	}

	defer func() {
		if err != nil {
			if inErr := os.RemoveAll(path); inErr != nil {
				log.G(ctx).WithError(inErr).Debug("failed to delete: " + path)
			}
		}
	}()

	defer func() {
		if err := f.Close(); err != nil {
			log.G(ctx).WithError(err).Debug("failed to close: " + path)
		}
	}()

	if err := f.Truncate(size); err != nil {
		return errors.Wrapf(err, "failed to truncate: %s", path)
	}

	return nil
}

// GetBlockDeviceSize returns the size of the specified block device.
func GetBlockDeviceSize(ctx context.Context, path string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, errors.Wrap(err, "error opening: "+path)
	}

	defer func() {
		if err := file.Close(); err != nil {
			log.G(ctx).WithError(err).Debug("error closing: " + path)
		}
	}()

	pos, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, errors.Wrap(err, "error seeking end of: "+path)
	}

	return pos, nil
}

// CopyEmptySparseFilesystem copies data chunks of a sparse source file into a
// destination file. It skips holes. Note that this is intended to copy a
// filesystem that has just been generated, so it only contains metadata blocks.
// Because of that, the source file must end with a hole. If it ends with data,
// the last chunk of data won't be copied.
func CopyEmptySparseFilesystem(source string, destination string) error {
	fin, err := os.OpenFile(source, os.O_RDONLY, 0)
	if err != nil {
		return errors.Wrap(err, "failed to open source file")
	}
	defer fin.Close()

	fout, err := os.OpenFile(destination, os.O_WRONLY, 0)
	if err != nil {
		return errors.Wrap(err, "failed to open destination file")
	}
	defer fout.Close()

	finInfo, err := fin.Stat()
	if err != nil {
		return errors.Wrap(err, "failed to stat source file")
	}

	finSize := finInfo.Size()

	var offset int64 = 0
	for {
		// Exit when the end of the file is reached
		if offset >= finSize {
			break
		}

		// Calculate bounds of the next data chunk
		chunkStart, err := fin.Seek(offset, SEEK_DATA)
		if (err != nil) || (chunkStart == -1) {
			// No more chunks left
			break
		}
		chunkEnd, err := fin.Seek(chunkStart, SEEK_HOLE)
		if (err != nil) || (chunkEnd == -1) {
			break
		}
		chunkSize := chunkEnd - chunkStart
		offset = chunkEnd

		// Read contents of this data chunk
		//nolint:staticcheck //TODO: SA1019: os.SEEK_SET has been deprecated since Go 1.7: Use io.SeekStart, io.SeekCurrent, and io.SeekEnd.
		_, err = fin.Seek(chunkStart, os.SEEK_SET)
		if err != nil {
			return errors.Wrap(err, "failed to seek set in source file")
		}

		chunkData := make([]byte, chunkSize)
		count, err := fin.Read(chunkData)
		if err != nil {
			return errors.Wrap(err, "failed to read source file")
		}
		if int64(count) != chunkSize {
			return errors.Wrap(err, "not enough data read from source file")
		}

		// Write data to destination file
		//nolint:staticcheck //TODO: SA1019: os.SEEK_SET has been deprecated since Go 1.7: Use io.SeekStart, io.SeekCurrent, and io.SeekEnd.
		_, err = fout.Seek(chunkStart, os.SEEK_SET)
		if err != nil {
			return errors.Wrap(err, "failed to seek destination file")
		}
		_, err = fout.Write(chunkData)
		if err != nil {
			return errors.Wrap(err, "failed to write destination file")
		}
	}

	return nil
}

// MkfsExt4Command runs mkfs.ext4 with the provided arguments
func MkfsExt4Command(args []string) error {
	cmd := exec.Command("mkfs.ext4", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "failed to execute mkfs.ext4: %s", string(output))
	}
	return nil
}

func FormatDiskExt4(ctx context.Context, source string) error {
	tempDir, err := os.MkdirTemp("", "ext4-dir")
	if err != nil {
		return errors.Wrapf(err, "failed to create temporary folder: %s", source)
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			log.G(ctx).WithError(err).Debugf("failed to delete temporary folder: %s", tempDir)
		}
	}()

	deviceSize, err := GetBlockDeviceSize(ctx, source)
	if err != nil {
		return fmt.Errorf("error getting size of: %s: %w", source, err)
	}
	if deviceSize == 0 {
		return fmt.Errorf("invalid size obtained for: %s", source)
	}

	tempExt4File := filepath.Join(tempDir, "ext4.img")

	if err = CreateSparseEmptyFile(ctx, tempExt4File, deviceSize); err != nil {
		return fmt.Errorf("failed to create sparse filesystem file: %w", err)
	}

	// 4.3. Format it as ext4
	if err = MkfsExt4Command([]string{tempExt4File}); err != nil {
		return fmt.Errorf("mkfs.ext4 failed to format %s: %w", tempExt4File, err)
	}

	// 4.4. Sparse copy of the filesystem into the encrypted block device
	if err = CopyEmptySparseFilesystem(tempExt4File, source); err != nil {
		return fmt.Errorf("failed to do sparse copy: %w", err)
	}
	return nil
}
