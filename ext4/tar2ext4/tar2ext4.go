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

const (
	whiteoutPrefix = ".wh."
	opaqueWhiteout = ".wh..wh..opq"
)

func ConvertTarToExt4GPT(r io.Reader, fs *compactext4.Writer, options ...Option) (int, error) {
	var p params
	for _, opt := range options {
		opt(&p)
	}

	t := tar.NewReader(bufio.NewReader(r))
	log.G(context.Background()).Info("got a new compact writer")
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
	log.G(context.Background()).Info("got a new compact writer")
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

func findNextBlockAddress(addressInBytes uint64) uint64 {
	return (addressInBytes / compactext4.BlockSizeInBytes) + 1
}

// Katiewasnothere: Convert overloads the previous Convert by allowing multiple readers
// TODO katiewasnothere: maybe add some option to create GPT
// TODO katiewasnothere: make a new package for creating GPT header + entries so we can reuse in gcs to read later
func ConvertMultiple(multipleReaders []io.Reader, w io.ReadWriteSeeker, options ...Option) error {
	var p params
	for _, opt := range options {
		opt(&p)
	}
	if len(multipleReaders) > 128 {
		return fmt.Errorf("readers exceeds max number of partitions for a GPT disk: %d", len(multipleReaders))
	}
	ctx := context.Background()
	// calculate starting position
	sizeOfEntryArrayBytes := gpt.SizeOfPartitionEntry * len(multipleReaders)
	log.G(ctx).WithField("size of gpt entry array in Bytes", sizeOfEntryArrayBytes).Info("entry array size")

	totalGPTInfoSizeInBytes := sizeOfEntryArrayBytes + gpt.SizeOfHeaderInBytes
	totaMetadataSizeInBytes := totalGPTInfoSizeInBytes + gpt.SizeOfPMBRInBytes
	log.G(ctx).WithField("total size of header in Bytes", totaMetadataSizeInBytes).Info("total header size")

	firstUseableLBA := findNextBlockAddress(uint64(totaMetadataSizeInBytes))
	firstUseableByte := firstUseableLBA * compactext4.BlockSizeInBytes
	log.G(ctx).WithField("firstUseableLBA", firstUseableLBA).Info("firstUseableLBA")
	log.G(ctx).WithField("firstUseableByte", firstUseableByte).Info("firstUseableByte")

	if _, err := w.Seek(int64(firstUseableByte), io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to the first useable LBA in disk with %v", err)
	}

	entries := make([]gpt.PartitionEntry, len(multipleReaders))
	typeGuid, err := guid.FromString("C12A7328-F81F-11D2-BA4B-00A0C93EC93B") // EFI system partition
	if err != nil {
		return fmt.Errorf("failed to construct EFI system partition guid type with %v", typeGuid)
	}
	startLBA := firstUseableLBA

	// write partitions out and create entries
	fs := compactext4.NewWriter(w, p.ext4opts...)
	for i, r := range multipleReaders {
		entryGuid, err := guid.NewV4()
		if err != nil {
			return fmt.Errorf("failed to construct unique guid for partition entry")
		}

		// TODO katiewasnothere: get a new writer at specific placement so that we don't accidentally overwrite info
		sizeInBits, err := ConvertTarToExt4GPT(r, fs, options...)
		if err != nil {
			return err
		}
		log.G(ctx).WithField("size at this point", sizeInBits).Info("writter position")

		sizeInBlocks := sizeInBits / 4096
		log.G(ctx).WithField("size at this point in blocks", sizeInBlocks).Infof("writter position %v", sizeInBlocks)

		entry := gpt.PartitionEntry{
			PartitionTypeGUID:   typeGuid,
			UniquePartitionGUID: entryGuid,
			StartingLBA:         startLBA,
			EndingLBA:           uint64(sizeInBlocks), // inclusive
			Attributes:          0,
			PartitionName:       [72]byte{}, // TODO katiewasnothere: make this
		}
		log.G(ctx).WithField("entry", entry).Info("entry")

		entries[i] = entry

		// update the startLBA for the next entry
		startLBA = uint64(sizeInBlocks) + 1

	}

	// TODO katiewasnothere: need to round this
	sizeWrittenInBytes := (int64(fs.Position()) / 8)
	lastUseableLBA := uint64(sizeWrittenInBytes / compactext4.BlockSizeInBytes)
	log.G(ctx).WithField("lastUseableLBA", lastUseableLBA).Info("lastUseableLBA")

	altEntriesArrayStart := findNextBlockAddress(uint64(sizeOfEntryArrayBytes))
	log.G(ctx).WithField("altEntriesArrayStart", altEntriesArrayStart).Info("altEntriesArrayStart")

	_, err = w.Seek(int64(altEntriesArrayStart), io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek file: %v", err)
	}
	log.G(ctx).WithField("just seeked", lastUseableLBA).Info("just seeked")

	// calculate where the alternate GPT header goes at end and write
	for _, e := range entries {
		if err := binary.Write(w, binary.LittleEndian, e); err != nil {
			return fmt.Errorf("failed to write backup entry array with: %v", err)
		}
	}
	log.G(ctx).WithField("just wrote", lastUseableLBA).Info("just wrote")

	sizeAfterBackupEntryArrayInBytes, err := w.Seek(int64(int(altEntriesArrayStart)+sizeOfEntryArrayBytes), io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek file: %v", err)
	}
	log.G(ctx).WithField("just wrote sizeAfterBackupEntryArrayInBytes", sizeAfterBackupEntryArrayInBytes).Info("just wrote")

	// TODO katiewasnothere: round to nearest block
	alternateHeaderLBA := findNextBlockAddress(uint64(sizeAfterBackupEntryArrayInBytes))
	// write header
	log.G(ctx).WithField("just wrote alternateHeaderLBA", alternateHeaderLBA).Info("just wrote")

	// katiewasnothere: wait to write to disk at the end so we can fill in SizeInLBA
	diskGUID, err := guid.NewV4()
	if err != nil {
		return fmt.Errorf("failed to create unique disk guid with %v", err)
	}

	// TODO: need to have written the entries before this point
	altEntriesCheckSum, err := getChecksumPartitionEntryArray(w, uint32(altEntriesArrayStart), uint32(sizeOfEntryArrayBytes))
	if err != nil {
		return err
	}
	altGPTHeader := gpt.Header{
		Signature:                [8]byte{54, 52, 41, 50, 20, 49, 46, 45}, // ASCII string "EFI PART" // TODO katiewasnothere: not sure if this works? since we write out little endian
		Revision:                 1,
		HeaderSize:               compactext4.BlockSizeInBytes, // BlockSize / bitsperbyte
		HeaderCRC32:              0,                            // TODO katiewasnothere: set to 0 then calculate crc32 checksum and replace
		ReservedMiddle:           0,
		MyLBA:                    alternateHeaderLBA, // LBA of this header
		AlternateLBA:             0,                  // TODO katiewasnothere: update to backup when we've written it
		FirstUsableLBA:           firstUseableLBA,    // TODO katiewasnothere: calculate after you know the size of the partition entries
		LastUsableLBA:            lastUseableLBA,     // TODO katiewasnothere: calculate after you know the size of the disk
		DiskGUID:                 diskGUID,
		PartitionEntryLBA:        altEntriesArrayStart, // right after this header
		NumberOfPartitionEntries: uint32(len(multipleReaders)),
		SizeOfPartitionEntry:     128,                // Must be set to a value of 128 x 2^n, where n is >= 0
		PartitionEntryArrayCRC32: altEntriesCheckSum, // TODO katiewasnothere: need to fix this up later
		ReservedEnd:              [420]byte{},
	}
	altGPTHeader.HeaderCRC32, err = getHeaderChecksum(altGPTHeader)
	if err != nil {
		return err
	}
	log.G(ctx).WithField("altGPTHeader", altGPTHeader).Info("altGPTHeader")

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
		StartingCHS:   [3]byte{00, 02, 00},            // TODO katiewasnothere: not sure if this works? since we write out little endian
		OSType:        0xEE,                           // GPT protective
		StartingLBA:   0x00000001,                     // LBA of the GPT header
		SizeInLBA:     uint32(alternateHeaderLBA) + 1, // TODO katiewasnothere: set once we know later, NOT SURE IF THIS IS RIGHT
	}

	// write the protectiveMBR
	if err := binary.Write(w, binary.LittleEndian, pMBR); err != nil {
		return fmt.Errorf("failed to write backup header with: %v", err)
	}

	// TODO: need to have written the entries before this point
	entriesCheckSum, err := getChecksumPartitionEntryArray(w, 2, uint32(sizeOfEntryArrayBytes))
	if err != nil {
		return err
	}
	log.G(ctx).WithField("just wrote", entriesCheckSum).Info("just wrote")

	hGPT := gpt.Header{
		Signature:                [8]byte{54, 52, 41, 50, 20, 49, 46, 45}, // ASCII string "EFI PART" // TODO katiewasnothere: not sure if this works? since we write out little endian
		Revision:                 1,
		HeaderSize:               compactext4.BlockSizeInBytes, // BlockSize / bitsperbyte
		HeaderCRC32:              0,                            // TODO katiewasnothere: set to 0 then calculate crc32 checksum and replace
		ReservedMiddle:           0,
		MyLBA:                    0x00000001,         // LBA of this header
		AlternateLBA:             alternateHeaderLBA, // TODO katiewasnothere: update to backup when we've written it
		FirstUsableLBA:           firstUseableLBA,    // TODO katiewasnothere: calculate after you know the size of the partition entries
		LastUsableLBA:            lastUseableLBA,     // TODO katiewasnothere: calculate after you know the size of the disk
		DiskGUID:                 diskGUID,
		PartitionEntryLBA:        0x00000002, // right after this header
		NumberOfPartitionEntries: uint32(len(multipleReaders)),
		SizeOfPartitionEntry:     128,             // Must be set to a value of 128 x 2^n, where n is >= 0
		PartitionEntryArrayCRC32: entriesCheckSum, // TODO katiewasnothere: need to fix this up later
		ReservedEnd:              [420]byte{},
	}
	hGPT.HeaderCRC32, err = getHeaderChecksum(hGPT)
	if err != nil {
		return err
	}
	log.G(ctx).WithField("hGPT", hGPT).Info("hGPT")

	// write the protectiveMBR
	if err := binary.Write(w, binary.LittleEndian, hGPT); err != nil {
		return fmt.Errorf("failed to write backup header with: %v", err)
	}

	// write header

	// write partition entries
	for _, e := range entries {
		if err := binary.Write(w, binary.LittleEndian, e); err != nil {
			return fmt.Errorf("failed to write backup entry array with: %v", err)
		}
	}

	// TODO katiewasnothere: append dmverity to each layer and final disk???

	// TODO katiewasnothere: first lets try making a fake disk so that I can see the way the bits needs to be laid out

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
	if err := binary.Write(buf, binary.LittleEndian, header); err != nil {
		return 0, err
	}
	checksum := crc32.ChecksumIEEE(buf.Bytes())
	ctx := context.Background()

	log.G(ctx).WithField("checksum", checksum).Info("checksum")

	return checksum, nil
}

func getChecksumPartitionEntryArray(w io.ReadWriteSeeker, entryArrayLBA uint32, readLengthInBytes uint32) (uint32, error) {
	ctx := context.Background()
	currentBytePosition, err := w.Seek(0, io.SeekCurrent) // TODO katiewasnothere, maybe I can use this to figure out where the end of the previously written disk is?
	if err != nil {
		return 0, err
	}

	log.G(ctx).WithField("currentBytePosition", currentBytePosition).Info("currentBytePosition")

	// seek to position of entry array
	entryArrayOffsetInBytes := int64(entryArrayLBA * compactext4.BlockSizeInBytes)
	log.G(ctx).WithField("entryArrayOffsetInBytes", entryArrayOffsetInBytes).Info("entryArrayOffsetInBytes")

	_, err = w.Seek(entryArrayOffsetInBytes, io.SeekStart) // TODO katiewasnothere, maybe I can use this to figure out where the end of the previously written disk is?
	if err != nil {
		return 0, err
	}
	buf := make([]byte, readLengthInBytes)
	if err := binary.Read(w, binary.LittleEndian, buf); err != nil {
		return 0, err
	}

	// calculate crc32 hash
	checksum := crc32.ChecksumIEEE(buf)

	// set writer back to position we started at
	// TODO katiewasnothere: I'm not really sure if seeking from current position above actually does what I think it does
	if _, err := w.Seek(currentBytePosition, io.SeekStart); err != nil {
		return 0, err
	}
	return checksum, nil
}

// TODO katiewasnothere: need to write something to read the GPT file
func ReadGPTHeader(r io.Reader) (gpt.Header, error) {
	return gpt.Header{}, nil
}

func ReadGPTPartitionArray(r io.Reader) ([]gpt.PartitionEntry, error) {
	return nil, nil
}

// TODO katiewasnothere: this needs to accommodate the fact that there are multiple partitions

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
