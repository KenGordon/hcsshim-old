# Shimlike

This package contains a proof-of-concept shim-like program that exposes an API to create and manage containers inside a decoupled UVM.

## Building

In order to properly run, various changes have been made to other packages. Importantly, changes have been made to `init.c` and `vsockexec.c` that make it possible to run a decoupled UVM. As such, the rootfs must be rebuilt specifically.

The following commands should be run inside WSL in the base folder of the cloned repository:

```sh
make clean
make BASE=<LSG Folder>/<Version>/outputs/build/images_lcow/lcow/core-image-minimal-lcow.tar
go install github.com/Microsoft/hcsshim/cmd/tar2ext4
gzip -df ./out/rootfs.tar.gz
tar2ext4.exe -i ./out/rootfs.tar -o ./out/rootfs.vhd -vhd
cp ./out/rootfs.vhd /mnt/c/ContainerPlat/LinuxBootFiles/rootfs.vhd
echo Done
```

The Shimlike can be built by running `go build` in the `cmd/shimlike` folder with `$GOOS="windows"`.

The protobuf files were built using the following commands:

```sh
protoc --gogo_opt=paths=source_relative --gogo_out=plugins=grpc:. -I vendor/ --proto_path=. cmd/shimlike/proto/api.proto
protoc --gogo_opt=paths=source_relative --gogo_out=plugins=grpc:. -I vendor/ --proto_path=. internal/tools/shimlikeclient/proto/api.proto
```

## Usage

The Shimlike accepts two arguments: a pipe address on which to listen for connections, and the UVM ID to connect to.

```powershell
.\shimlike.exe \\.\pipe\vm_pipe 3b4ef355-09ff-5081-b8d2-9ec9662860da
```

After launching, the Shimlike will listen for connections on the specified pipe.

### Usage with UVMBoot

Important changes have also been made to UVMBoot to ensure decoupled functionality. As such, UVMBoot must be rebuilt specifically.

UVMBoot should be launched with the following arguments (The `--mount-scsi` arguments may be passed in any order):

```powershell
.\uvmboot.exe --debug lcow --root-fs-type vhd --boot-files-path C:\ContainerPlat\LinuxBootFiles\ --vpmem-max-count 0 --mount-scsi '<path_to_scratch_disk>,,w' --mount-scsi '<path_to_pause_image>' --mount-scsi '<path_to_container_images>'...
```

### Usage with Shimlike Client

Shimlike Client (internal/tools/shimlikeclient) is a very simple client that, currently, is hard-coded to create 3 containers inside a decoupled UVM which ping microsoft.com. It can be built by running `go build` in the `internal/tools/shimlikeclient` folder with `$GOOS="windows"`.

The Shimlike Client accepts the same arguments as the Shimlike:

```powershell
.\shimlikeclient.exe \\.\pipe\vm_pipe 3b4ef355-09ff-5081-b8d2-9ec9662860da
```

After launching, the Shimlike Client will prompt the user to enter a Namespace ID and NIC ID. These IDs can be found in the UVMBoot logs:

```
[GET]=>[/namespaces/<namespace ID here>]
...
Adding NIC with ID <NIC ID here>
```

Ideally, the Shimlike Client will be updated to allow the user to specify operations to perform in the decoupled UVM.