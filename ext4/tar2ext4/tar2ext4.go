package tar2ext4

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/pkg/errors"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/ext4/dmverity"
	"github.com/Microsoft/hcsshim/ext4/internal/compactext4"
	"github.com/Microsoft/hcsshim/ext4/internal/format"
	"github.com/Microsoft/hcsshim/ext4/internal/gpt"
	"github.com/Microsoft/hcsshim/internal/log"
)

type params struct {
	convertWhiteout bool
	appendVhdFooter bool
	appendDMVerity  bool
	ext4opts        []compactext4.Option
}

// Option is the type for optional parameters to Convert.
type Option func(*params)

// ConvertWhiteout instructs the converter to convert OCI-style whiteouts
// (beginning with .wh.) to overlay-style whiteouts.
func ConvertWhiteout(p *params) {
	p.convertWhiteout = true
}

// AppendVhdFooter instructs the converter to add a fixed VHD footer to the
// file.
func AppendVhdFooter(p *params) {
	p.appendVhdFooter = true
}

// AppendDMVerity instructs the converter to add a dmverity merkle tree for
// the ext4 filesystem after the filesystem and before the optional VHD footer
func AppendDMVerity(p *params) {
	p.appendDMVerity = true
}

// InlineData instructs the converter to write small files into the inode
// structures directly. This creates smaller images but currently is not
// compatible with DAX.
func InlineData(p *params) {
	p.ext4opts = append(p.ext4opts, compactext4.InlineData)
}

// MaximumDiskSize instructs the writer to limit the disk size to the specified
// value. This also reserves enough metadata space for the specified disk size.
// If not provided, then 16GB is the default.
func MaximumDiskSize(size int64) Option {
	return func(p *params) {
		p.ext4opts = append(p.ext4opts, compactext4.MaximumDiskSize(size))
	}
}

func StartWritePosition(start int64) Option {
	return func(p *params) {
		p.ext4opts = append(p.ext4opts, compactext4.StartWritePosition(start))
	}
}

const (
	whiteoutPrefix = ".wh."
	opaqueWhiteout = ".wh..wh..opq"
)

func ConvertTarToExt4GPT(r io.Reader, w io.ReadWriteSeeker, options ...Option) (int, error) {
	var p params
	for _, opt := range options {
		opt(&p)
	}
	log.G(context.Background()).Infof("params: %v", p)

	fs := compactext4.NewWriter(w, p.ext4opts...)
	log.G(context.Background()).Infof("start position: %v", fs.Position())

	t := tar.NewReader(bufio.NewReader(r))
	for {
		hdr, err := t.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}

		if err = fs.MakeParents(hdr.Name); err != nil {
			return 0, errors.Wrapf(err, "failed to ensure parent directories for %s", hdr.Name)
		}

		if p.convertWhiteout {
			dir, name := path.Split(hdr.Name)
			if strings.HasPrefix(name, whiteoutPrefix) {
				if name == opaqueWhiteout {
					// Update the directory with the appropriate xattr.
					f, err := fs.Stat(dir)
					if err != nil {
						return 0, errors.Wrapf(err, "failed to stat parent directory of whiteout %s", hdr.Name)
					}
					f.Xattrs["trusted.overlay.opaque"] = []byte("y")
					err = fs.Create(dir, f)
					if err != nil {
						return 0, errors.Wrapf(err, "failed to create opaque dir %s", hdr.Name)
					}
				} else {
					// Create an overlay-style whiteout.
					f := &compactext4.File{
						Mode:     compactext4.S_IFCHR,
						Devmajor: 0,
						Devminor: 0,
					}
					err = fs.Create(path.Join(dir, name[len(whiteoutPrefix):]), f)
					if err != nil {
						return 0, errors.Wrapf(err, "failed to create whiteout file for %s", hdr.Name)
					}
				}

				continue
			}
		}

		if hdr.Typeflag == tar.TypeLink {
			err = fs.Link(hdr.Linkname, hdr.Name)
			if err != nil {
				return 0, err
			}
		} else {
			f := &compactext4.File{
				Mode:     uint16(hdr.Mode),
				Atime:    hdr.AccessTime,
				Mtime:    hdr.ModTime,
				Ctime:    hdr.ChangeTime,
				Crtime:   hdr.ModTime,
				Size:     hdr.Size,
				Uid:      uint32(hdr.Uid),
				Gid:      uint32(hdr.Gid),
				Linkname: hdr.Linkname,
				Devmajor: uint32(hdr.Devmajor),
				Devminor: uint32(hdr.Devminor),
				Xattrs:   make(map[string][]byte),
			}
			for key, value := range hdr.PAXRecords {
				const xattrPrefix = "SCHILY.xattr."
				if strings.HasPrefix(key, xattrPrefix) {
					f.Xattrs[key[len(xattrPrefix):]] = []byte(value)
				}
			}

			var typ uint16
			switch hdr.Typeflag {
			case tar.TypeReg, tar.TypeRegA:
				typ = compactext4.S_IFREG
			case tar.TypeSymlink:
				typ = compactext4.S_IFLNK
			case tar.TypeChar:
				typ = compactext4.S_IFCHR
			case tar.TypeBlock:
				typ = compactext4.S_IFBLK
			case tar.TypeDir:
				typ = compactext4.S_IFDIR
			case tar.TypeFifo:
				typ = compactext4.S_IFIFO
			}
			f.Mode &= ^compactext4.TypeMask
			f.Mode |= typ
			err = fs.Create(hdr.Name, f)
			if err != nil {
				return 0, err
			}
			_, err = io.Copy(fs, t)
			if err != nil {
				return 0, err
			}
		}
	}

	// TODO katiewasnothere: can we close here? is this okay?
	if err := fs.Close(); err != nil {
		return 0, err
	}

	return int(fs.Position()), nil
}

// ConvertTarToExt4 writes a compact ext4 file system image that contains the files in the
// input tar stream.
func ConvertTarToExt4(r io.Reader, w io.ReadWriteSeeker, options ...Option) error {
	var p params
	for _, opt := range options {
		opt(&p)
	}

	t := tar.NewReader(bufio.NewReader(r))
	fs := compactext4.NewWriter(w, p.ext4opts...)
	for {
		hdr, err := t.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if err = fs.MakeParents(hdr.Name); err != nil {
			return errors.Wrapf(err, "failed to ensure parent directories for %s", hdr.Name)
		}

		if p.convertWhiteout {
			dir, name := path.Split(hdr.Name)
			if strings.HasPrefix(name, whiteoutPrefix) {
				if name == opaqueWhiteout {
					// Update the directory with the appropriate xattr.
					f, err := fs.Stat(dir)
					if err != nil {
						return errors.Wrapf(err, "failed to stat parent directory of whiteout %s", hdr.Name)
					}
					f.Xattrs["trusted.overlay.opaque"] = []byte("y")
					err = fs.Create(dir, f)
					if err != nil {
						return errors.Wrapf(err, "failed to create opaque dir %s", hdr.Name)
					}
				} else {
					// Create an overlay-style whiteout.
					f := &compactext4.File{
						Mode:     compactext4.S_IFCHR,
						Devmajor: 0,
						Devminor: 0,
					}
					err = fs.Create(path.Join(dir, name[len(whiteoutPrefix):]), f)
					if err != nil {
						return errors.Wrapf(err, "failed to create whiteout file for %s", hdr.Name)
					}
				}

				continue
			}
		}

		if hdr.Typeflag == tar.TypeLink {
			err = fs.Link(hdr.Linkname, hdr.Name)
			if err != nil {
				return err
			}
		} else {
			f := &compactext4.File{
				Mode:     uint16(hdr.Mode),
				Atime:    hdr.AccessTime,
				Mtime:    hdr.ModTime,
				Ctime:    hdr.ChangeTime,
				Crtime:   hdr.ModTime,
				Size:     hdr.Size,
				Uid:      uint32(hdr.Uid),
				Gid:      uint32(hdr.Gid),
				Linkname: hdr.Linkname,
				Devmajor: uint32(hdr.Devmajor),
				Devminor: uint32(hdr.Devminor),
				Xattrs:   make(map[string][]byte),
			}
			for key, value := range hdr.PAXRecords {
				const xattrPrefix = "SCHILY.xattr."
				if strings.HasPrefix(key, xattrPrefix) {
					f.Xattrs[key[len(xattrPrefix):]] = []byte(value)
				}
			}

			var typ uint16
			switch hdr.Typeflag {
			case tar.TypeReg, tar.TypeRegA:
				typ = compactext4.S_IFREG
			case tar.TypeSymlink:
				typ = compactext4.S_IFLNK
			case tar.TypeChar:
				typ = compactext4.S_IFCHR
			case tar.TypeBlock:
				typ = compactext4.S_IFBLK
			case tar.TypeDir:
				typ = compactext4.S_IFDIR
			case tar.TypeFifo:
				typ = compactext4.S_IFIFO
			}
			f.Mode &= ^compactext4.TypeMask
			f.Mode |= typ
			err = fs.Create(hdr.Name, f)
			if err != nil {
				return err
			}
			_, err = io.Copy(fs, t)
			if err != nil {
				return err
			}
		}
	}
	return fs.Close()
}

// Convert wraps ConvertTarToExt4 and conditionally computes (and appends) the file image's cryptographic
// hashes (merkle tree) or/and appends a VHD footer.
func Convert(r io.Reader, w io.ReadWriteSeeker, options ...Option) error {
	var p params
	for _, opt := range options {
		opt(&p)
	}

	if err := ConvertTarToExt4(r, w, options...); err != nil {
		return err
	}

	if p.appendDMVerity {
		if err := dmverity.ComputeAndWriteHashDevice(w, w); err != nil {
			return err
		}
	}

	if p.appendVhdFooter {
		return ConvertToVhd(w)
	}
	return nil
}

func findNextLogicalBlock(bytePosition uint64) uint64 {
	block := uint64(bytePosition / compactext4.BlockSizeLogical)
	/*if bytePosition%compactext4.BlockSizeLogical == 0 {
		return block
	}*/

	return block + 1
}

// Katiewasnothere: Convert overloads the previous Convert by allowing multiple readers
func ConvertMultiple(multipleReaders []io.Reader, w io.ReadWriteSeeker, options ...Option) error {
	var p params
	for _, opt := range options {
		opt(&p)
	}
	if len(multipleReaders) > 128 {
		return fmt.Errorf("readers exceeds max number of partitions for a GPT disk: %d", len(multipleReaders))
	}
	// calculate starting position
	var (
		sizeOfEntryArrayBytes = gpt.SizeOfPartitionEntry * len(multipleReaders) // 128

		totalGPTInfoSizeInBytes = sizeOfEntryArrayBytes + gpt.SizeOfHeaderInBytes
		totaMetadataSizeInBytes = totalGPTInfoSizeInBytes + gpt.SizeOfPMBRInBytes
	)
	// first useable LBA must be >=34, 32 reserved blocks for partition entry array
	firstUseableLBA := findNextLogicalBlock(uint64(totaMetadataSizeInBytes))
	if firstUseableLBA < 34 {
		firstUseableLBA = 34
	}
	firstUseableByte := firstUseableLBA * compactext4.BlockSizeLogical
	if _, err := w.Seek(int64(firstUseableByte), io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to the first useable LBA in disk with %v", err)
	}

	// katiewasnothere: partitions should be aligned to the physical block size
	entries := make([]gpt.PartitionEntry, len(multipleReaders))              // 128
	typeGuid, err := guid.FromString("0FC63DAF-8483-4772-8E79-3D69D8477DE4") // Linux filesystem data
	if err != nil {
		return fmt.Errorf("failed to construct EFI system partition guid type with %v", typeGuid)
	}
	startLBA := firstUseableLBA
	endingLBA := firstUseableLBA

	// write partitions out and create entries
	for i, r := range multipleReaders {
		entryGuid, err := guid.NewV4()
		if err != nil {
			return fmt.Errorf("failed to construct unique guid for partition entry")
		}

		_, err = w.Seek(int64(startLBA*compactext4.BlockSizeLogical), io.SeekStart)
		if err != nil {
			return fmt.Errorf("failed to seek file: %v", err)
		}
		startOffset := int64(startLBA * compactext4.BlockSizeLogical)
		currentOptions := append(options, StartWritePosition(startOffset))
		currentByte, err := ConvertTarToExt4GPT(r, w, currentOptions...)
		if err != nil {
			return err
		}
		endingLBA = startLBA + uint64(currentByte/compactext4.BlockSizeLogical) // startLBA +  // firstUseableLBA + uint64(currentByte/compactext4.BlockSizeLogical)
		entry := gpt.PartitionEntry{
			PartitionTypeGUID:   typeGuid,
			UniquePartitionGUID: entryGuid,
			StartingLBA:         startLBA,
			EndingLBA:           endingLBA, // inclusive
			Attributes:          0,
			PartitionName:       [72]byte{}, // TODO katiewasnothere: make this
		}
		entries[i] = entry

		// update the startLBA for the next entry
		startLBA = uint64(endingLBA) + 1
	}
	// TODO katiewasnothere: fix this if we never go through loop for partitions
	lastUseableLBA := findNextLogicalBlock(endingLBA * compactext4.BlockSizeLogical)
	lastUseableByte := lastUseableLBA * compactext4.BlockSizeLogical

	altEntriesArrayStartLBA := findNextLogicalBlock(uint64(lastUseableByte))
	altEntriesArrayStart := altEntriesArrayStartLBA * compactext4.BlockSizeLogical

	_, err = w.Seek(int64(altEntriesArrayStart), io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek file: %v", err)
	}

	// calculate where the alternate GPT header goes at end and write
	//TODO katiewasnothere: there must be a min of 16384 bytes reserved for gpt partition entry array
	for _, e := range entries {
		if err := binary.Write(w, binary.LittleEndian, e); err != nil {
			return fmt.Errorf("failed to write backup entry array with: %v", err)
		}
	}

	sizeAfterBackupEntryArrayInBytes, err := w.Seek(int64(int(altEntriesArrayStart)+sizeOfEntryArrayBytes), io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek file: %v", err)
	}

	// TODO katiewasnothere: round to nearest block
	// TODO katiewasnothere: need to align array to at least
	alternateHeaderLBA := findNextLogicalBlock(uint64(sizeAfterBackupEntryArrayInBytes))
	alternateHeaderInBytes := alternateHeaderLBA * compactext4.BlockSizeLogical
	_, err = w.Seek(int64(alternateHeaderInBytes), io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek file: %v", err)
	}

	diskGUID, err := guid.NewV4()
	if err != nil {
		return fmt.Errorf("failed to create unique disk guid with %v", err)
	}

	// TODO: need to have written the entries before this point
	altEntriesCheckSum, err := getChecksumPartitionEntryArray(w, uint32(altEntriesArrayStartLBA), uint32(sizeOfEntryArrayBytes))
	if err != nil {
		return err
	}
	altGPTHeader := gpt.Header{
		Signature:                0x5452415020494645, // ASCII string "EFI PART"
		Revision:                 0x00010000,
		HeaderSize:               92, // size of this header
		HeaderCRC32:              0,  // set to 0 then calculate crc32 checksum and replace
		ReservedMiddle:           0,
		MyLBA:                    alternateHeaderLBA, // LBA of this header
		AlternateLBA:             1,
		FirstUsableLBA:           firstUseableLBA,
		LastUsableLBA:            lastUseableLBA,
		DiskGUID:                 diskGUID,
		PartitionEntryLBA:        altEntriesArrayStartLBA,      // right after this header
		NumberOfPartitionEntries: uint32(len(multipleReaders)), // 128,                     //uint32(len(multipleReaders)),
		SizeOfPartitionEntry:     128,                          // Must be set to a value of 128 x 2^n, where n is >= 0
		PartitionEntryArrayCRC32: altEntriesCheckSum,
		ReservedEnd:              [420]byte{},
	}
	altGPTHeader.HeaderCRC32, err = getHeaderChecksum(altGPTHeader)
	if err != nil {
		return err
	}

	// write the alternate header
	if err := binary.Write(w, binary.LittleEndian, altGPTHeader); err != nil {
		return fmt.Errorf("failed to write backup header with: %v", err)
	}

	// update the size in protectiveMBR
	_, err = w.Seek(0, io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek file: %v", err)
	}
	// Write protectiveMBR
	pMBR := gpt.ProtectiveMBR{
		BootCode:               [440]byte{}, // unused by UEFI systems
		UniqueMBRDiskSignature: 0,           // set to 0
		Unknown:                0,
		PartitionRecord:        [4]gpt.PartitionMBR{},
		Signature:              0xAA55,
	}
	pMBR.PartitionRecord[0] = gpt.PartitionMBR{
		BootIndicator: 0,
		StartingCHS:   [3]byte{0x00, 0x02, 0x00},  // TODO katiewasnothere: not sure if this works? since we write out little endian
		OSType:        0xEE,                       // GPT protective
		EndingCHS:     [3]byte{0xff, 0xff, 0xff},  // katiewasnothere: use actual size
		StartingLBA:   1,                          // LBA of the GPT header
		SizeInLBA:     uint32(alternateHeaderLBA), // Set to the size of the disk minus one. TODO katiewasnothere: set once we know later, NOT SURE IF THIS IS RIGHT, is this inclusive????
	}

	// write the protectiveMBR
	if err := binary.Write(w, binary.LittleEndian, pMBR); err != nil {
		return fmt.Errorf("failed to write backup header with: %v", err)
	}

	_, err = w.Seek(int64(2*compactext4.BlockSizeLogical), io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek file: %v", err)
	}

	// write partition entries
	for _, e := range entries {
		if err := binary.Write(w, binary.LittleEndian, e); err != nil {
			return fmt.Errorf("failed to write backup entry array with: %v", err)
		}
	}

	entriesCheckSum, err := getChecksumPartitionEntryArray(w, 2, uint32(sizeOfEntryArrayBytes))
	if err != nil {
		return err
	}

	_, err = w.Seek(int64(1*compactext4.BlockSizeLogical), io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek file: %v", err)
	}

	hGPT := gpt.Header{
		Signature:                0x5452415020494645, // ASCII string "EFI PART" // TODO katiewasnothere: not sure if this works? since we write out little endian
		Revision:                 0x00010000,
		HeaderSize:               uint32(gpt.HeaderSize),
		HeaderCRC32:              0, // set to 0 then calculate crc32 checksum and replace
		ReservedMiddle:           0,
		MyLBA:                    1, // LBA of this header
		AlternateLBA:             alternateHeaderLBA,
		FirstUsableLBA:           firstUseableLBA,
		LastUsableLBA:            lastUseableLBA,
		DiskGUID:                 diskGUID,
		PartitionEntryLBA:        2,                            // right after this header
		NumberOfPartitionEntries: uint32(len(multipleReaders)), //128, //uint32(len(multipleReaders)),
		SizeOfPartitionEntry:     128,                          // Must be set to a value of 128 x 2^n, where n is >= 0
		PartitionEntryArrayCRC32: entriesCheckSum,
		ReservedEnd:              [420]byte{},
	}
	hGPT.HeaderCRC32, err = getHeaderChecksum(hGPT)
	if err != nil {
		return err
	}

	// write the gpt header
	if err := binary.Write(w, binary.LittleEndian, hGPT); err != nil {
		return fmt.Errorf("failed to write backup header with: %v", err)
	}

	// TODO katiewasnothere: append dmverity to each layer and final disk???

	/*if p.appendDMVerity {
		if err := dmverity.ComputeAndWriteHashDevice(w, w); err != nil {
			return err
		}
	}*/

	if p.appendVhdFooter {
		return ConvertToVhd(w)
	}
	return nil
}

func getHeaderChecksum(header gpt.Header) (uint32, error) {
	buf := &bytes.Buffer{}
	// do not include reserved field
	if err := binary.Write(buf, binary.LittleEndian, header); err != nil {
		return 0, err
	}

	checksum := crc32.ChecksumIEEE(buf.Bytes()[:gpt.HeaderSize])
	return checksum, nil
}

func getChecksumPartitionEntryArray(w io.ReadWriteSeeker, entryArrayLBA uint32, readLengthInBytes uint32) (uint32, error) {
	currentBytePosition, err := w.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}

	// seek to position of entry array
	entryArrayOffsetInBytes := int64(entryArrayLBA * compactext4.BlockSizeLogical)
	_, err = w.Seek(entryArrayOffsetInBytes, io.SeekStart)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, readLengthInBytes)
	if err := binary.Read(w, binary.LittleEndian, buf); err != nil {
		return 0, err
	}

	// calculate crc32 hash
	checksum := crc32.ChecksumIEEE(buf)

	if _, err := w.Seek(currentBytePosition, io.SeekStart); err != nil {
		return 0, err
	}
	return checksum, nil
}

func ReadPMBR(r io.ReadSeeker) (gpt.ProtectiveMBR, error) {
	current, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return gpt.ProtectiveMBR{}, fmt.Errorf("failed to seek the current byte: %v", err)
	}

	pMBRByteLocation := 0 * compactext4.BlockSizeLogical
	if _, err := r.Seek(int64(pMBRByteLocation), io.SeekStart); err != nil {
		return gpt.ProtectiveMBR{}, fmt.Errorf("failed to seek to pMBR byte location %d with: %v", pMBRByteLocation, err)
	}
	pMBR := gpt.ProtectiveMBR{}
	if err := binary.Read(r, binary.LittleEndian, &pMBR); err != nil {
		return gpt.ProtectiveMBR{}, fmt.Errorf("failed to read pMBR: %v", err)
	}

	if _, err := r.Seek(current, io.SeekStart); err != nil {
		return gpt.ProtectiveMBR{}, fmt.Errorf("failed to seek back to current byte position: %v", err)
	}
	return pMBR, nil
}

func ReadGPTHeader(r io.ReadSeeker, lba uint64) (gpt.Header, error) {
	current, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return gpt.Header{}, fmt.Errorf("failed to seek the current byte: %v", err)
	}

	headerByteLocation := lba * compactext4.BlockSizeLogical
	if _, err := r.Seek(int64(headerByteLocation), io.SeekStart); err != nil {
		return gpt.Header{}, fmt.Errorf("failed to seek to header byte location %d with: %v", headerByteLocation, err)
	}

	header := gpt.Header{}
	if err := binary.Read(r, binary.LittleEndian, &header); err != nil {
		return gpt.Header{}, fmt.Errorf("failed to read gpt header: %v", err)
	}
	if _, err := r.Seek(current, io.SeekStart); err != nil {
		return gpt.Header{}, fmt.Errorf("failed to seek back to current byte position: %v", err)
	}

	return header, nil
}

func ReadGPTPartitionArray(r io.ReadSeeker, entryArrayLBA uint64, numEntries uint32) ([]gpt.PartitionEntry, error) {
	// seek to position of entry array
	currentBytePosition, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	entryArrayOffsetInBytes := int64(entryArrayLBA * compactext4.BlockSizeLogical)
	_, err = r.Seek(entryArrayOffsetInBytes, io.SeekStart)
	if err != nil {
		return nil, err
	}

	entries := make([]gpt.PartitionEntry, numEntries)
	for i := 0; i < int(numEntries); i++ {
		entry := gpt.PartitionEntry{}
		if err := binary.Read(r, binary.LittleEndian, &entry); err != nil {
			return nil, fmt.Errorf("failed to read entry index %d with: %v", i, err)
		}
		entries[i] = entry
	}

	// set writer back to position we started at
	// TODO katiewasnothere: I'm not really sure if seeking from current position above actually does what I think it does
	if _, err := r.Seek(currentBytePosition, io.SeekStart); err != nil {
		return nil, err
	}
	return entries, nil
}

func ReadPartitionRaw(r io.ReadSeeker, partitionLBA, endingLBA uint64) ([]byte, error) {
	// seek to position of entry array
	currentBytePosition, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	partitionOffset := partitionLBA * compactext4.BlockSizeLogical
	endingOffsetEndBlock := (endingLBA * compactext4.BlockSizeLogical) + compactext4.BlockSizeLogical
	if _, err := r.Seek(int64(partitionOffset), io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to header byte location %d with: %v", partitionOffset, err)
	}

	buf := make([]byte, endingOffsetEndBlock-partitionOffset)
	if err := binary.Read(r, binary.LittleEndian, &buf); err != nil {
		return nil, fmt.Errorf("failed to read gpt header: %v", err)
	}

	// set writer back to position we started at
	if _, err := r.Seek(currentBytePosition, io.SeekStart); err != nil {
		return nil, err
	}
	return buf, nil
}

// ReadExt4SuperBlock reads and returns ext4 super block from VHD
//
// The layout on disk is as follows:
// | Group 0 padding     | - 1024 bytes
// | ext4 SuperBlock     | - 1 block
// | Group Descriptors   | - many blocks
// | Reserved GDT Blocks | - many blocks
// | Data Block Bitmap   | - 1 block
// | inode Bitmap        | - 1 block
// | inode Table         | - many blocks
// | Data Blocks         | - many blocks
//
// More details can be found here https://ext4.wiki.kernel.org/index.php/Ext4_Disk_Layout
//
// Our goal is to skip the Group 0 padding, read and return the ext4 SuperBlock
func ReadExt4SuperBlock(vhdPath string) (*format.SuperBlock, error) {
	vhd, err := os.OpenFile(vhdPath, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer vhd.Close()

	// Skip padding at the start
	if _, err := vhd.Seek(1024, io.SeekStart); err != nil {
		return nil, err
	}
	var sb format.SuperBlock
	if err := binary.Read(vhd, binary.LittleEndian, &sb); err != nil {
		return nil, err
	}
	// Make sure the magic bytes are correct.
	if sb.Magic != format.SuperBlockMagic {
		return nil, errors.New("not an ext4 file system")
	}
	return &sb, nil
}

func ReadExt4SuperBlockFromPartition(vhdPath string, partitionLBA int64) (*format.SuperBlock, error) {
	partitionOffset := partitionLBA * compactext4.BlockSizeLogical
	vhd, err := os.OpenFile(vhdPath, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer vhd.Close()

	// Skip padding at the start
	if _, err := vhd.Seek(partitionOffset+1024, io.SeekStart); err != nil {
		return nil, err
	}
	var sb format.SuperBlock
	if err := binary.Read(vhd, binary.LittleEndian, &sb); err != nil {
		return nil, err
	}
	// Make sure the magic bytes are correct.
	if sb.Magic != format.SuperBlockMagic {
		return nil, fmt.Errorf("not an ext4 file system, got %v", sb)
	}
	return &sb, nil
}

// ConvertAndComputeRootDigest writes a compact ext4 file system image that contains the files in the
// input tar stream, computes the resulting file image's cryptographic hashes (merkle tree) and returns
// merkle tree root digest. Convert is called with minimal options: ConvertWhiteout and MaximumDiskSize
// set to dmverity.RecommendedVHDSizeGB.
func ConvertAndComputeRootDigest(r io.Reader) (string, error) {
	out, err := ioutil.TempFile("", "")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %s", err)
	}
	defer func() {
		_ = os.Remove(out.Name())
	}()

	options := []Option{
		ConvertWhiteout,
		MaximumDiskSize(dmverity.RecommendedVHDSizeGB),
	}
	if err := ConvertTarToExt4(r, out, options...); err != nil {
		return "", fmt.Errorf("failed to convert tar to ext4: %s", err)
	}

	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("failed to seek start on temp file when creating merkle tree: %s", err)
	}

	tree, err := dmverity.MerkleTree(bufio.NewReaderSize(out, dmverity.MerkleTreeBufioSize))
	if err != nil {
		return "", fmt.Errorf("failed to create merkle tree: %s", err)
	}

	hash := dmverity.RootHash(tree)
	return fmt.Sprintf("%x", hash), nil
}

// ConvertToVhd converts given io.WriteSeeker to VHD, by appending the VHD footer with a fixed size.
func ConvertToVhd(w io.WriteSeeker) error {
	size, err := w.Seek(0, io.SeekEnd) // TODO katiewasnothere, maybe I can use this to figure out where the end of the previously written disk is?
	if err != nil {
		return err
	}
	return binary.Write(w, binary.BigEndian, makeFixedVHDFooter(size))
}
