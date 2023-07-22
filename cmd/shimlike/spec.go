package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/opencontainers/runtime-spec/specs-go"
)

func assignNamespaces(spec *specs.Spec, pid int) {
	spec.Linux.Namespaces = []specs.LinuxNamespace{
		{
			Type: specs.PIDNamespace,
			Path: fmt.Sprintf("/proc/%d/ns/pid", pid),
		},
		{
			Type: specs.IPCNamespace,
			Path: fmt.Sprintf("/proc/%d/ns/ipc", pid),
		},
		{
			Type: specs.UTSNamespace,
			Path: fmt.Sprintf("/proc/%d/ns/uts", pid),
		},
		{
			Type: specs.MountNamespace,
		},
		{
			Type: specs.NetworkNamespace,
			Path: fmt.Sprintf("/proc/%d/ns/net", pid),
		},
	}
}

func readSpec() (*linuxHostedSystem, error) {
	binPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	f, err := os.Open(filepath.Join(filepath.Dir(binPath), "spec.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var spec specs.Spec
	if err := json.NewDecoder(f).Decode(&spec); err != nil {
		return nil, err
	}
	l := &linuxHostedSystem{
		SchemaVersion:    &hcsschema.Version{Major: 2, Minor: 1},
		OciSpecification: &spec,
	}
	return l, nil
}

// Modifies the spec to be a workload container spec.
func applyContainerSpec(doc *linuxHostedSystem) {
	doc.OciSpecification.Mounts = append(doc.OciSpecification.Mounts, specs.Mount{
		Destination: "/sys/fs/cgroup", Type: "cgroup", Source: "cgroup", Options: []string{"nosuid", "noexec", "nodev", "relatime", "ro"},
	})
	doc.OciSpecification.Annotations["io.kubernetes.cri.container-type"] = "container"
}

// Modifies the spec to be a sandbox spec.
func applySandboxSpec(doc *linuxHostedSystem) {
	doc.OciSpecification.Process.User = specs.User{
		UID: 0,
		GID: 0,
	}
	doc.OciSpecification.Process.Args = []string{"/pause"}
	doc.OciSpecification.Process.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	doc.OciSpecification.Process.Cwd = "/"
	doc.OciSpecification.Process.Rlimits = []specs.POSIXRlimit{
		{Type: "RLIMIT_NOFILE", Hard: 1024, Soft: 1024},
	}
	doc.OciSpecification.Process.NoNewPrivileges = true

	doc.OciSpecification.Mounts = append(doc.OciSpecification.Mounts, specs.Mount{
		Destination: "/run", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "strictatime", "mode=755", "size=65536k"},
	})
	doc.OciSpecification.Annotations["io.kubernetes.cri.container-type"] = "sandbox"
}
