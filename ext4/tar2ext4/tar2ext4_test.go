package tar2ext4

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"testing"

	"archive/tar"
	"os"
	"strings"
	"time"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/ext4/internal/gpt"
)

// Test_UnorderedTarExpansion tests that we are correctly able to expand a layer tar file
// which has one or many files in an unordered fashion. By unordered we mean that the
// entry of a file shows up during an expansion before the entry of one of the parent
// directories of that file.  In such cases we create that parent directory with same
// permissions as its parent and then later on fix the permissions when we actually see
// the entry of that parent directory.
func Test_UnorderedTarExpansion(t *testing.T) {
	tmpTarFilePath := filepath.Join(os.TempDir(), "test-layer.tar")
	layerTar, err := os.Create(tmpTarFilePath)
	if err != nil {
		t.Fatalf("failed to create output file: %s", err)
	}
	defer os.Remove(tmpTarFilePath)

	tw := tar.NewWriter(layerTar)
	var files = []struct {
		path, body string
	}{
		{"foo/.wh.bar.txt", "inside bar.txt"},
		{"data/", ""},
		{"root.txt", "inside root.txt"},
		{"foo/", ""},
		{"A/.wh..wh..opq", ""},
		{"A/B/b.txt", "inside b.txt"},
		{"A/a.txt", "inside a.txt"},
		{"A/", ""},
		{"A/B/", ""},
	}
	for _, file := range files {
		var hdr *tar.Header
		if strings.HasSuffix(file.path, "/") {
			hdr = &tar.Header{
				Name:       file.path,
				Mode:       0777,
				Size:       0,
				ModTime:    time.Now(),
				AccessTime: time.Now(),
				ChangeTime: time.Now(),
			}
		} else {
			hdr = &tar.Header{
				Name:       file.path,
				Mode:       0777,
				Size:       int64(len(file.body)),
				ModTime:    time.Now(),
				AccessTime: time.Now(),
				ChangeTime: time.Now(),
			}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(file.path, "/") {
			if _, err := tw.Write([]byte(file.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	// Now try to import this tar and verify that there is no failure.
	if _, err := layerTar.Seek(0, 0); err != nil {
		t.Fatalf("failed to seek file: %s", err)
	}

	opts := []Option{AppendVhdFooter, ConvertWhiteout}
	tmpVhdPath := filepath.Join(os.TempDir(), "test-vhd.vhdx")
	layerVhd, err := os.Create(tmpVhdPath)
	if err != nil {
		t.Fatalf("failed to create output VHD: %s", err)
	}
	defer os.Remove(tmpVhdPath)

	if err := Convert(layerTar, layerVhd, opts...); err != nil {
		t.Fatalf("failed to convert tar to layer vhd: %s", err)
	}
}

func Test_TarHardlinkToSymlink(t *testing.T) {
	tmpTarFilePath := filepath.Join(os.TempDir(), "test-layer.tar")
	layerTar, err := os.Create(tmpTarFilePath)
	if err != nil {
		t.Fatalf("failed to create output file: %s", err)
	}
	defer os.Remove(tmpTarFilePath)

	tw := tar.NewWriter(layerTar)

	var files = []struct {
		path     string
		typeFlag byte
		linkName string
		body     string
	}{
		{
			path: "/tmp/zzz.txt",
			body: "inside /tmp/zzz.txt",
		},
		{
			path:     "/tmp/xxx.txt",
			linkName: "/tmp/zzz.txt",
			typeFlag: tar.TypeSymlink,
		},
		{
			path:     "/tmp/yyy.txt",
			linkName: "/tmp/xxx.txt",
			typeFlag: tar.TypeLink,
		},
	}
	for _, file := range files {
		hdr := &tar.Header{
			Name:       file.path,
			Typeflag:   file.typeFlag,
			Linkname:   file.linkName,
			Mode:       0777,
			Size:       int64(len(file.body)),
			ModTime:    time.Now(),
			AccessTime: time.Now(),
			ChangeTime: time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if file.body != "" {
			if _, err := tw.Write([]byte(file.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	// Now try to import this tar and verify that there is no failure.
	if _, err := layerTar.Seek(0, 0); err != nil {
		t.Fatalf("failed to seek file: %s", err)
	}

	opts := []Option{AppendVhdFooter, ConvertWhiteout}
	tmpVhdPath := filepath.Join(os.TempDir(), "test-vhd.vhdx")
	layerVhd, err := os.Create(tmpVhdPath)
	if err != nil {
		t.Fatalf("failed to create output VHD: %s", err)
	}
	defer os.Remove(tmpVhdPath)

	if err := Convert(layerTar, layerVhd, opts...); err != nil {
		t.Fatalf("failed to convert tar to layer vhd: %s", err)
	}
}

func Test_GPT(t *testing.T) {
	fileReaders := []io.Reader{}
	guids := []string{}

	// create numLayers test layers
	numLayers := 5
	for i := 0; i < numLayers; i++ {
		g, err := guid.NewV4()
		if err != nil {
			t.Fatalf("failed to create guid for layer: %v", err)
		}
		guids = append(guids, g.String())

		name := "test-layer-" + strconv.Itoa(i) + ".tar"
		tmpTarFilePath := filepath.Join(os.TempDir(), name)
		layerTar, err := os.Create(tmpTarFilePath)
		if err != nil {
			t.Fatalf("failed to create output file: %s", err)
		}
		defer os.Remove(tmpTarFilePath)
		fileReaders = append(fileReaders, layerTar)

		tw := tar.NewWriter(layerTar)
		var files = []struct {
			path     string
			typeFlag byte
			linkName string
			body     string
		}{
			{
				path: "/tmp/zzz.txt",
				body: "inside /tmp/zzz.txt",
			},
			{
				path:     "/tmp/xxx.txt",
				linkName: "/tmp/zzz.txt",
				typeFlag: tar.TypeSymlink,
			},
			{
				path:     "/tmp/yyy.txt",
				linkName: "/tmp/xxx.txt",
				typeFlag: tar.TypeLink,
			},
		}
		for _, file := range files {
			hdr := &tar.Header{
				Name:       file.path,
				Typeflag:   file.typeFlag,
				Linkname:   file.linkName,
				Mode:       0777,
				Size:       int64(len(file.body)),
				ModTime:    time.Now(),
				AccessTime: time.Now(),
				ChangeTime: time.Now(),
			}
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatal(err)
			}
			if file.body != "" {
				if _, err := tw.Write([]byte(file.body)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		// Go to the beginning of the tar file so that we can read it correctly
		if _, err := layerTar.Seek(0, 0); err != nil {
			t.Fatalf("failed to seek file: %s", err)
		}
	}

	opts := []Option{AppendVhdFooter, ConvertWhiteout}
	tmpVhdPath := filepath.Join(os.TempDir(), "test-vhd.vhd")
	layerVhd, err := os.Create(tmpVhdPath)
	if err != nil {
		t.Fatalf("failed to create output VHD: %s", err)
	}
	defer os.Remove(tmpVhdPath)

	dg, err := guid.NewV4()
	if err != nil {
		t.Fatalf("failed to create guid for layer: %v", err)
	}
	diskGuid := dg.String()

	if err := ConvertMultiple(fileReaders, layerVhd, guids, diskGuid, opts...); err != nil {
		t.Fatalf("failed to convert tar to layer vhd: %s", err)
	}

	// primary header is always in lba 1
	header, err := ReadGPTHeader(layerVhd, 1)
	if err != nil {
		t.Fatalf("failed to read header from tar file %v", err)
	}
	if err := validateGPTHeader(&header, diskGuid); err != nil {
		t.Fatalf("gpt header is corrupt: %v", err)
	}

	pmbr, err := ReadPMBR(layerVhd)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePMBR(&pmbr); err != nil {
		t.Fatalf("pmbr is corrupt: %v", err)
	}

	altHeader, err := ReadGPTHeader(layerVhd, header.AlternateLBA)
	if err != nil {
		t.Fatalf("failed to read header from tar file %v", err)
	}
	if err := validateGPTHeader(&altHeader, diskGuid); err != nil {
		t.Fatalf("gpt alt header is corrupt: %v", err)
	}

	_, err = ReadGPTPartitionArray(layerVhd, header.PartitionEntryLBA, header.NumberOfPartitionEntries)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ReadGPTPartitionArray(layerVhd, altHeader.PartitionEntryLBA, altHeader.NumberOfPartitionEntries)
	if err != nil {
		t.Fatal(err)
	}
}

func validateGPTHeader(h *gpt.Header, diskGUIDString string) error {
	if h.Signature != gpt.HeaderSignature {
		return fmt.Errorf("expected %v for the header signature, instead got %v", gpt.HeaderSignature, h.Signature)
	}
	if h.Revision != gpt.HeaderRevision {
		return fmt.Errorf("expected %v for the header revision, instead got %v", gpt.HeaderRevision, h.Revision)
	}
	if h.HeaderSize != gpt.HeaderSize {
		return fmt.Errorf("expected %v for the header size, instead got %v", gpt.HeaderSize, h.HeaderSize)
	}
	if h.ReservedMiddle != 0 {
		return fmt.Errorf("expected reserved middle bytes, instead got %v", h.ReservedMiddle)
	}
	diskGUID, err := guid.FromString(diskGUIDString)
	if err != nil {
		return fmt.Errorf("error converting supplied disk guid %v", err)
	}
	if h.DiskGUID != diskGUID {
		return fmt.Errorf("expected to get disk guid of %v, instead got %v", diskGUIDString, h.DiskGUID.String())
	}
	if h.ReservedEnd != [420]byte{} {
		return fmt.Errorf("expected to find reserved bytes at end of header, instead found %v", h.ReservedEnd)
	}

	// TODO katiewasnothere: check the header and partition entry checksums
	// you'll need to zero them out in the og header to calculate correctly

	return nil
}

func validatePMBR(pmbr *gpt.ProtectiveMBR) error {
	if pmbr.BootCode != [440]byte{} {
		return fmt.Errorf("expected unused boot code in pmbr, instead found %v", pmbr.BootCode)
	}
	if pmbr.UniqueMBRDiskSignature != 0 {
		return fmt.Errorf("expected field to be set to 0, instead found %v", pmbr.UniqueMBRDiskSignature)
	}
	if pmbr.Unknown != 0 {
		return fmt.Errorf("expected field to be set to 0, instead found %v", pmbr.Unknown)
	}
	if len(pmbr.PartitionRecord) != 4 {
		return fmt.Errorf("expected 4 partition records, instead found %v", len(pmbr.PartitionRecord))
	}
	if pmbr.Signature != gpt.ProtectiveMBRSignature {
		return fmt.Errorf("expected pmbr signature to be %v, instead got %v", gpt.ProtectiveMBRSignature, pmbr.Signature)
	}

	pr := pmbr.PartitionRecord[0]
	if pr.BootIndicator != 0 {
		return fmt.Errorf("expected partition record's boot indicator to be 0, instead found %v", pr.BootIndicator)
	}
	if pr.StartingCHS != [3]byte{0x00, 0x02, 0x00} {
		return fmt.Errorf("expected startign CHS to be %v, instead found %v", [3]byte{0x00, 0x02, 0x00}, pr.StartingCHS)
	}
	if pr.OSType != 0xEE {
		return fmt.Errorf("expected partition record's os type to be %v, instead found %v", 0xEE, pr.OSType)
	}
	if pr.StartingLBA != 1 {
		return fmt.Errorf("expected startign LBA to be 1, instead got %v", pr.StartingLBA)
	}
	// TODO katiewasnothere: should I check the ending chs?

	return nil
}
