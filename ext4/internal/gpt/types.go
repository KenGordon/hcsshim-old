package gpt

import (
	"encoding/binary"

	"github.com/Microsoft/go-winio/pkg/guid"
)

// See uefi specification at https://uefi.org/specifications for details

var (
	SizeOfPMBRInBytes    = binary.Size(ProtectiveMBR{})
	SizeOfHeaderInBytes  = binary.Size(Header{})
	SizeOfPartitionEntry = binary.Size(PartitionEntry{})
)

// katiewasnothere: little endian
// ProtectiveMBR is 512 bytes, which is == BlockSizeLogical
type ProtectiveMBR struct {
	BootCode               [440]byte       // 440 bytes
	UniqueMBRDiskSignature uint32          // 4 bytes, unused set to zero
	Unknown                uint16          // 2 bytes, unused set to zero
	PartitionRecord        [4]PartitionMBR // 16*4 bytes, array of four MBR parititions, one actual record and 3 records set to zero
	Signature              uint16          // 2 bytes, set to 0xAA55
	// Reserved               [3584]byte      // rest of the logical block if any
}

// PartitionMBR is 16 bytes
type PartitionMBR struct {
	BootIndicator uint8   // 1 byte, set to 0 to indicate non-bootable partition
	StartingCHS   [3]byte // 3 bytes, set to 0x000200, corresponding to starting LBA field
	OSType        uint8   // 1 byte, set to 0xEE (GPT Protective)
	EndingCHS     [3]byte // 3 bytes, set to the last logical block of the disk of 0xffffff if not possible to represent the value in this field
	StartingLBA   uint32  // 4 bytes, set to 0x00000001 (LBA of the GPT partition header)
	SizeInLBA     uint32  // 4 bytes, set to the size of the disk - 1 or 0xffffffff if size is too big to represent here
}

// GPT info: GPT primary table must be in LBA 1 (aka the second logical block) and the
// secondary (alternate) table must be in the last LBA of the disk
type Header struct {
	Signature                uint64    // 8 bytes
	Revision                 uint32    // 4 bytes, 0x00010000
	HeaderSize               uint32    // 4 bytes, must be greater than or equal to 92 and must be less than or equal to the logical block size
	HeaderCRC32              uint32    // 4 bytes, CRC32 checksum for the GPT header structure. Computed by setting this to zer0, and computing the 32 bit crc for headersize in bytes
	ReservedMiddle           uint32    // 4 bytes, must be zzero
	MyLBA                    uint64    // 8 bytes, The LBA that contains this data structure
	AlternateLBA             uint64    // 8 bytes, LBA of the alternate GPT header
	FirstUsableLBA           uint64    // 8 bytes, the first logical block that may be used by a GPT entry
	LastUsableLBA            uint64    // 8 bytes, the last usable logical block to be used by a GPT entry
	DiskGUID                 guid.GUID // 16 bytes, used to uniquely identify the disk
	PartitionEntryLBA        uint64    // 8 bytes, the starting LBA of the GPT Entries Array
	NumberOfPartitionEntries uint32    // 4 bytes, the number of partition entries
	SizeOfPartitionEntry     uint32    // 4 bytes, the size in bytes of each of the partition entry structures in the Entry Array. Must be set to a value of 128 x 2^n, where n is >= 0
	PartitionEntryArrayCRC32 uint32    // 4 bytes, the crc32 of the entry array. Starts at PartitionEntryLBA and is computed over a byte length of NumberOfPartitionEntries * SizeOfPartitionEntry
	ReservedEnd              [420]byte // rest of the block, BlockSize in bytes - 92, must be set to 0
}

// To check if a GPT table is valid:
// - Check the signature
// - check the header CRC
// - Check that the MyLBA entry points to the LBA that contains the GPT table
// - Check that CRC of the Entry Arrray
// - If the GPT is the primary table, check the AlternateLBA to see if it is a valid GPT table as well

type PartitionEntry struct {
	PartitionTypeGUID   guid.GUID // 16 bytes, unique ID that defines the purpose and type of this partition.
	UniquePartitionGUID guid.GUID // 16 bytes, unique for every partition entry, must be assigned when the entry is created
	StartingLBA         uint64    // 8 bytes, Starting LBA of the parition defined by this entry
	EndingLBA           uint64    // 8 bytes, Ending LBA of the partition
	Attributes          uint64    // 8 bytes, attribute bits
	PartitionName       [72]byte  // 72 bytes, null terminated string with the name
	// Reserved                      // SizeOfPartitionEntry - 128, rest of the size, must be zero
}

// hash/crc32 package here https://pkg.go.dev/hash/crc32

// The layout of a GPT disk is as follows:
// | Protective MBR 				| - 1 block
// | Partition Table HDR 			| - 1 block
// | Partition Entry Array 			| - Size of Partition Entry * number of partitions
// | Partition 0 					|
// | ...... 						|
// | Partition n 					|
// | Backup Partition Entry Array 	|
// | Backup Partition Table HDR 	| - Last 1 block

// TODO katiewasnothere:
// need the ability to write a block
// need the ability to write zeros

// I don't know the starting or ending partition until after we've written it to disk
// So maybe we can make a partition for each layer given and not write them until after
// we've written the ext4s. So we'd need to seek into the disk based on the size of the
// entry array that we think we're gonna make.

// Do I really need the partition name?
