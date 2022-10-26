package computestorage

import (
	"bytes"
	"fmt"
	"os/exec"

	"github.com/Microsoft/go-winio/pkg/guid"
)

func bcdExec(storePath string, args ...string) error {
	var out bytes.Buffer
	argsArr := []string{"/store", storePath, "/offline"}
	argsArr = append(argsArr, args...)
	cmd := exec.Command("bcdedit.exe", argsArr...)
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("bcd command (%s) failed: %s", cmd, err)
	}
	return nil
}

// A registry configuration required for the uvm.
func setBcdRestartOnFailure(storePath string) error {
	return bcdExec(storePath, "/set", "{default}", "restartonfailure", "yes")
}

// bcdedit /store <BCD> /create {763e9fea-502d-434f-aad9-5fabe9c91a7b} /d "CimFS Device Options" /device
// bcdedit /store <BCD> /set {763e9fea-502d-434f-aad9-5fabe9c91a7b} cimfsrootdirectory \cim\boot.cim
// bcdedit /store <BCD> /set {763e9fea-502d-434f-aad9-5fabe9c91a7b} cimfsparentdevice vmbus={c63c9bdf-5fa5-4208-b03f-6b458b365592}

// bcdedit /store <BCD> /create {e1787220-d17f-49e7-977a-d8fe4c8537e2} /d "Composite Device Options" /device
// bcdedit /store <BCD> /set {e1787220-d17f-49e7-977a-d8fe4c8537e2} primarydevice hd_partition=d:
// bcdedit /store <BCD> /set {e1787220-d17f-49e7-977a-d8fe4c8537e2} secondarydevice cimfs={b890454c-80de-4e98-a7ab-56b74b4fbd0c},{763e9fea-502d-434f-aad9-5fabe9c91a7b}

// bcdedit /store <BCD> /set {default} osdevice composite=0,{e1787220-d17f-49e7-977a-d8fe4c8537e2}
// bcdedit /store <BCD> /set {default} device composite=0,{e1787220-d17f-49e7-977a-d8fe4c8537e2}

// Where \cim\boot.cim is the path to the CIM in the VSMB share and D: is the drive letter of the mounted VHD for the scratch root. Rather than mounting the VHD, you can also specify the disk and partition guids like this if you know them ahead of time: gpt_partition={ecf536ca-f50e-11e9-9962-70bc105c9fe8};{53ebea8c-a069-4a67-b3e0-ad94e7e433ee}

func setBcdCimBootDevice(storePath, cimPathRelativeToVSMB string, diskID, partitionID guid.GUID) error {
	if err := bcdExec(storePath, "/create", "{763e9fea-502d-434f-aad9-5fabe9c91a7b}", "/d", "CimFS Device Options", "/device"); err != nil {
		return err
	}

	if err := bcdExec(storePath, "/set", "{763e9fea-502d-434f-aad9-5fabe9c91a7b}", "cimfsrootdirectory", fmt.Sprintf("\\%s", cimPathRelativeToVSMB)); err != nil {
		return err
	}

	if err := bcdExec(storePath, "/set", "{763e9fea-502d-434f-aad9-5fabe9c91a7b}", "cimfsparentdevice", "vmbus={c63c9bdf-5fa5-4208-b03f-6b458b365592}"); err != nil {
		return err
	}

	if err := bcdExec(storePath, "/create", "{e1787220-d17f-49e7-977a-d8fe4c8537e2}", "/d", "Composite Device Options", "/device"); err != nil {
		return err
	}

	partitionStr := fmt.Sprintf("gpt_partition={%s};{%s}", diskID, partitionID)
	if err := bcdExec(storePath, "/set", "{e1787220-d17f-49e7-977a-d8fe4c8537e2}", "primarydevice", partitionStr); err != nil {
		return err
	}

	if err := bcdExec(storePath, "/set", "{e1787220-d17f-49e7-977a-d8fe4c8537e2}", "secondarydevice", "cimfs={b890454c-80de-4e98-a7ab-56b74b4fbd0c},{763e9fea-502d-434f-aad9-5fabe9c91a7b}"); err != nil {
		return err
	}

	if err := bcdExec(storePath, "/set", "{default}", "device", "composite=0,{e1787220-d17f-49e7-977a-d8fe4c8537e2}"); err != nil {
		return err
	}

	if err := bcdExec(storePath, "/set", "{default}", "osdevice", "composite=0,{e1787220-d17f-49e7-977a-d8fe4c8537e2}"); err != nil {
		return err
	}

	// Since our UVM file are stored under UtilityVM\Files directory inside the CIM we must prepend that
	// directory in front of paths used by bootmgr
	if err := bcdExec(storePath, "/set", "{default}", "path", "\\UtilityVM\\Files\\Windows\\System32\\winload.efi"); err != nil {
		return err
	}

	if err := bcdExec(storePath, "/set", "{default}", "systemroot", "\\UtilityVM\\Files\\Windows"); err != nil {
		return err
	}

	return nil
}

// A registry configuration required for the uvm.
func setBcdVmbusBootDevice(storePath string) error {
	vmbusDeviceStr := "vmbus={c63c9bdf-5fa5-4208-b03f-6b458b365592}"
	if err := bcdExec(storePath, "/set", "{default}", "device", vmbusDeviceStr); err != nil {
		return err
	}

	if err := bcdExec(storePath, "/set", "{default}", "osdevice", vmbusDeviceStr); err != nil {
		return err
	}

	if err := bcdExec(storePath, "/set", "{bootmgr}", "alternatebootdevice", vmbusDeviceStr); err != nil {
		return err
	}
	return nil
}

// A registry configuration required for the uvm.
func setBcdOsArcDevice(storePath string, diskID, partitionID guid.GUID) error {
	return bcdExec(storePath, "/set", "{default}", "osarcdevice", fmt.Sprintf("gpt_partition={%s};{%s}", diskID, partitionID))
}

func setDebugOn(storePath string) error {
	if err := bcdExec(storePath, "/set", "{default}", "testsigning", "on"); err != nil {
		return err
	}
	// if err := bcdExec(storePath, "/set", "{default}", "bootdebug", "on"); err != nil {
	// 	return err
	// }
	// if err := bcdExec(storePath, "/set", "{bootmgr}", "bootdebug", "on"); err != nil {
	// 	return err
	// }
	// if err := bcdExec(storePath, "/dbgsettings", "SERIAL", "DEBUGPORT:1", "BAUDRATE:115200"); err != nil {
	// 	return err
	// }
	// return bcdExec(storePath, "/set", "{default}", "debug", "on")
	return nil
}

// updateBcdStoreForBoot Updates the bcd store at path layerPath + UtilityVM\Files\EFI\Microsoft\Boot\BCD`
// to boot with the disk with given ID and given partitionID.
// cimPathRelativeToVSMB is the path of the cim which will be used for booting this UVM relative to the VSMB
// share. (Usually, the entire snapshots directory will be shared over VSMB, so if this is the cim-layers\1.cim under
// that directory, the value of `cimPathRelativeToVSMB` should be cim-layers\1.cim)
func updateBcdStoreForBoot(storePath string, cimPathRelativeToVSMB string, diskID, partitionID guid.GUID) error {
	if err := setBcdRestartOnFailure(storePath); err != nil {
		return err
	}

	// if err := setBcdVmbusBootDevice(storePath); err != nil {
	// 	return err
	// }
	if err := setBcdCimBootDevice(storePath, cimPathRelativeToVSMB, diskID, partitionID); err != nil {
		return err
	}

	// if err := setBcdOsArcDevice(storePath, diskID, partitionID); err != nil {
	// 	return err
	// }
	return setDebugOn(storePath)
}
