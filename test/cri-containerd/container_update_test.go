//go:build windows && functional
// +build windows,functional

package cri_containerd

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/Microsoft/hcsshim/internal/memory"
	"github.com/Microsoft/hcsshim/osversion"
	"github.com/Microsoft/hcsshim/pkg/annotations"
	"github.com/Microsoft/hcsshim/test/internal/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

const processorWeightMax = 10000

func calculateJobCPUWeight(processorWeight uint32) uint32 {
	if processorWeight == 0 {
		return 0
	}
	return 1 + uint32((8*processorWeight)/processorWeightMax)
}

//nolint:deadcode,unused // may be used in future tests
func calculateJobCPURate(hostProcs uint32, processorCount uint32) uint32 {
	rate := (processorCount * 10000) / hostProcs
	if rate == 0 {
		return 1
	}
	return rate
}

func Test_Container_UpdateResources_CPUShare(t *testing.T) {
	require.Build(t, osversion.V20H2)
	type config struct {
		name             string
		requiredFeatures []string
		runtimeHandler   string
		sandboxImage     string
		containerImage   string
		cmd              []string
		useAnnotation    bool
	}
	tests := []config{
		{
			name:             "WCOW_Process",
			requiredFeatures: []string{featureWCOWProcess, featureCRIUpdateContainer},
			runtimeHandler:   wcowProcessRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
			containerImage:   imageWindowsNanoserver,
			cmd:              []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
		},
		{
			name:             "WCOW_Hypervisor",
			requiredFeatures: []string{featureWCOWHypervisor, featureCRIUpdateContainer},
			runtimeHandler:   wcowHypervisorRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
			containerImage:   imageWindowsNanoserver,
			cmd:              []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
		},
		{
			name:             "LCOW",
			requiredFeatures: []string{featureLCOW, featureCRIUpdateContainer},
			runtimeHandler:   lcowRuntimeHandler,
			sandboxImage:     imageLcowK8sPause,
			containerImage:   imageLcowAlpine,
			cmd:              []string{"top"},
		},
	}
	for i := range tests {
		tt := tests[i]
		tt.requiredFeatures = append(tt.requiredFeatures, featureCRIPlugin)
		tt.useAnnotation = true
		tests = append(tests, tt)
	}

	for _, test := range tests {
		n := test.name
		if test.useAnnotation {
			n += "_Annotation"
		}
		t.Run(n, func(t *testing.T) {
			requireFeatures(t, test.requiredFeatures...)

			if test.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLCOWImages(t, []string{test.sandboxImage})
			} else if test.runtimeHandler == wcowHypervisorRuntimeHandler {
				pullRequiredImages(t, []string{test.sandboxImage})
			}

			podRequest := getRunPodSandboxRequest(t, test.runtimeHandler)

			client := newTestRuntimeClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			podID := runPodSandbox(t, client, ctx, podRequest)
			defer removePodSandbox(t, client, ctx, podID)
			defer stopPodSandbox(t, client, ctx, podID)

			containerRequest := &runtime.CreateContainerRequest{
				Config: &runtime.ContainerConfig{
					Metadata: &runtime.ContainerMetadata{
						Name: t.Name() + "-Container",
					},
					Image: &runtime.ImageSpec{
						Image: test.containerImage,
					},
					Command: test.cmd,
				},
				PodSandboxId:  podID,
				SandboxConfig: podRequest.Config,
			}

			containerID := createContainer(t, client, ctx, containerRequest)
			defer removeContainer(t, client, ctx, containerID)

			startContainer(t, client, ctx, containerID)
			defer stopContainer(t, client, ctx, containerID)

			// make request to increase cpu shares
			updateReq := &runtime.UpdateContainerResourcesRequest{
				ContainerId: containerID,
				Annotations: make(map[string]string),
			}

			var expected uint32 = 5000
			expectedStr := strconv.FormatUint(uint64(expected), 10)
			if test.useAnnotation {
				updateReq.Annotations[annotations.ContainerProcessorWeight] = expectedStr
			}
			if test.runtimeHandler == lcowRuntimeHandler {
				updateReq.Linux = &runtime.LinuxContainerResources{}
				if !test.useAnnotation {
					updateReq.Linux.CpuShares = int64(expected)
				}
			} else {
				updateReq.Windows = &runtime.WindowsContainerResources{}
				if !test.useAnnotation {
					updateReq.Windows.CpuShares = int64(expected)
				}
			}

			if _, err := client.UpdateContainerResources(ctx, updateReq); err != nil {
				t.Fatalf("updating container resources for %s with %v", containerID, err)
			}

			if test.runtimeHandler == lcowRuntimeHandler {
				checkLCOWResourceLimit(t, ctx, client, containerID, "/sys/fs/cgroup/cpu/cpu.shares", uint64(expected))
			} else {
				targetShimName := "k8s.io-" + podID
				jobExpectedValue := calculateJobCPUWeight(expected)
				checkWCOWResourceLimit(t, ctx, test.runtimeHandler, targetShimName, containerID, "cpu-weight", uint64(jobExpectedValue))
			}

			spec := getContainerOCISpec(t, client, ctx, containerID)
			if test.useAnnotation {
				checkAnnotation(t, spec, annotations.ContainerProcessorWeight, expectedStr)
			} else {
				var l uint64
				if test.runtimeHandler == lcowRuntimeHandler {
					l = *spec.Linux.Resources.CPU.Shares
				} else {
					l = uint64(*spec.Windows.Resources.CPU.Shares)
				}
				if l != uint64(expected) {
					t.Fatalf("got cpu shares %d, expected %d", l, expected)
				}
			}
		})
	}
}

func Test_Container_UpdateResources_CPUShare_NotRunning(t *testing.T) {
	type config struct {
		name             string
		requiredFeatures []string
		runtimeHandler   string
		sandboxImage     string
		containerImage   string
		cmd              []string
	}
	tests := []config{
		{
			name:             "WCOW_Process",
			requiredFeatures: []string{featureWCOWProcess, featureCRIUpdateContainer},
			runtimeHandler:   wcowProcessRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
			containerImage:   imageWindowsNanoserver,
			cmd:              []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
		},
		{
			name:             "WCOW_Hypervisor",
			requiredFeatures: []string{featureWCOWHypervisor, featureCRIUpdateContainer},
			runtimeHandler:   wcowHypervisorRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
			containerImage:   imageWindowsNanoserver,
			cmd:              []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
		},
		{
			name:             "LCOW",
			requiredFeatures: []string{featureLCOW, featureCRIUpdateContainer},
			runtimeHandler:   lcowRuntimeHandler,
			sandboxImage:     imageLcowK8sPause,
			containerImage:   imageLcowAlpine,
			cmd:              []string{"top"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireFeatures(t, test.requiredFeatures...)

			if test.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLCOWImages(t, []string{test.sandboxImage})
			} else if test.runtimeHandler == wcowHypervisorRuntimeHandler {
				pullRequiredImages(t, []string{test.sandboxImage})
			}

			podRequest := getRunPodSandboxRequest(t, test.runtimeHandler)

			client := newTestRuntimeClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			podID := runPodSandbox(t, client, ctx, podRequest)
			defer removePodSandbox(t, client, ctx, podID)
			defer stopPodSandbox(t, client, ctx, podID)

			containerRequest := &runtime.CreateContainerRequest{
				Config: &runtime.ContainerConfig{
					Metadata: &runtime.ContainerMetadata{
						Name: t.Name() + "-Container",
					},
					Image: &runtime.ImageSpec{
						Image: test.containerImage,
					},
					Command: test.cmd,
				},
				PodSandboxId:  podID,
				SandboxConfig: podRequest.Config,
			}

			containerID := createContainer(t, client, ctx, containerRequest)
			defer removeContainer(t, client, ctx, containerID)

			// make request to increase cpu shares == cpu weight
			updateReq := &runtime.UpdateContainerResourcesRequest{
				ContainerId: containerID,
			}

			var expected uint32 = 5000
			if test.runtimeHandler == lcowRuntimeHandler {
				updateReq.Linux = &runtime.LinuxContainerResources{
					CpuShares: int64(expected),
				}
			} else {
				updateReq.Windows = &runtime.WindowsContainerResources{
					CpuShares: int64(expected),
				}
			}

			if _, err := client.UpdateContainerResources(ctx, updateReq); err != nil {
				t.Fatalf("updating container resources for %s with %v", containerID, err)
			}

			startContainer(t, client, ctx, containerID)
			defer stopContainer(t, client, ctx, containerID)

			if test.runtimeHandler == lcowRuntimeHandler {
				checkLCOWResourceLimit(t, ctx, client, containerID, "/sys/fs/cgroup/cpu/cpu.shares", uint64(expected))
			} else {
				targetShimName := "k8s.io-" + podID
				jobExpectedValue := calculateJobCPUWeight(expected)
				checkWCOWResourceLimit(t, ctx, test.runtimeHandler, targetShimName, containerID, "cpu-weight", uint64(jobExpectedValue))
			}
		})
	}
}

func Test_Container_UpdateResources_Memory(t *testing.T) {
	type config struct {
		name             string
		requiredFeatures []string
		runtimeHandler   string
		sandboxImage     string
		containerImage   string
		cmd              []string
		useAnnotation    bool
	}
	tests := []config{
		{
			name:             "WCOW_Process",
			requiredFeatures: []string{featureWCOWProcess, featureCRIUpdateContainer},
			runtimeHandler:   wcowProcessRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
			containerImage:   imageWindowsNanoserver,
			cmd:              []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
		},
		{
			name:             "WCOW_Hypervisor",
			requiredFeatures: []string{featureWCOWHypervisor, featureCRIUpdateContainer},
			runtimeHandler:   wcowHypervisorRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
			containerImage:   imageWindowsNanoserver,
			cmd:              []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
		},
		{
			name:             "LCOW",
			requiredFeatures: []string{featureLCOW, featureCRIUpdateContainer},
			runtimeHandler:   lcowRuntimeHandler,
			sandboxImage:     imageLcowK8sPause,
			containerImage:   imageLcowAlpine,
			cmd:              []string{"top"},
		},
	}
	for i := range tests {
		tt := tests[i]
		tt.requiredFeatures = append(tt.requiredFeatures, featureCRIPlugin)
		tt.useAnnotation = true
		tests = append(tests, tt)
	}

	for _, test := range tests {
		n := test.name
		if test.useAnnotation {
			n += "_Annotation"
		}
		t.Run(n, func(t *testing.T) {
			requireFeatures(t, test.requiredFeatures...)

			if test.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLCOWImages(t, []string{test.sandboxImage})
			} else if test.runtimeHandler == wcowHypervisorRuntimeHandler {
				pullRequiredImages(t, []string{test.sandboxImage})
			}

			podRequest := getRunPodSandboxRequest(t, test.runtimeHandler)

			client := newTestRuntimeClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			podID := runPodSandbox(t, client, ctx, podRequest)
			defer removePodSandbox(t, client, ctx, podID)
			defer stopPodSandbox(t, client, ctx, podID)

			startingMemorySizeMiB := int64(768)
			var startingMemorySize int64 = startingMemorySizeMiB * memory.MiB
			containerRequest := &runtime.CreateContainerRequest{
				Config: &runtime.ContainerConfig{
					Metadata: &runtime.ContainerMetadata{
						Name: t.Name() + "-Container",
					},
					Image: &runtime.ImageSpec{
						Image: test.containerImage,
					},
					Command: test.cmd,
					Annotations: map[string]string{
						annotations.ContainerMemorySizeInMB: fmt.Sprintf("%d", startingMemorySizeMiB), // 768MB
					},
				},
				PodSandboxId:  podID,
				SandboxConfig: podRequest.Config,
			}

			containerID := createContainer(t, client, ctx, containerRequest)
			defer removeContainer(t, client, ctx, containerID)

			startContainer(t, client, ctx, containerID)
			defer stopContainer(t, client, ctx, containerID)

			// make request for memory limit
			updateReq := &runtime.UpdateContainerResourcesRequest{
				ContainerId: containerID,
			}

			newMemorySize := startingMemorySize / 2
			newMemorySizeStr := strconv.FormatUint(uint64(startingMemorySizeMiB/2), 10) // in MiB
			if test.useAnnotation {
				updateReq.Annotations = map[string]string{
					annotations.ContainerMemorySizeInMB: newMemorySizeStr,
				}
			}
			if test.runtimeHandler == lcowRuntimeHandler {
				updateReq.Linux = &runtime.LinuxContainerResources{}
				if !test.useAnnotation {
					updateReq.Linux.MemoryLimitInBytes = newMemorySize
				}
			} else {
				updateReq.Windows = &runtime.WindowsContainerResources{}
				if !test.useAnnotation {
					updateReq.Windows.MemoryLimitInBytes = newMemorySize
				}
			}

			if _, err := client.UpdateContainerResources(ctx, updateReq); err != nil {
				t.Fatalf("updating container resources for %s with %v", containerID, err)
			}

			if test.runtimeHandler == lcowRuntimeHandler {
				checkLCOWResourceLimit(t, ctx, client, containerID, "/sys/fs/cgroup/memory/memory.limit_in_bytes", uint64(newMemorySize))
			} else {
				targetShimName := "k8s.io-" + podID
				checkWCOWResourceLimit(t, ctx, test.runtimeHandler, targetShimName, containerID, "memory-limit", uint64(newMemorySize))
			}

			spec := getContainerOCISpec(t, client, ctx, containerID)
			if test.useAnnotation {
				checkAnnotation(t, spec, annotations.ContainerMemorySizeInMB, newMemorySizeStr)
			} else {
				var l uint64
				if test.runtimeHandler == lcowRuntimeHandler {
					l = uint64(*spec.Linux.Resources.Memory.Limit)
				} else {
					l = *spec.Windows.Resources.Memory.Limit
				}
				if l != uint64(newMemorySize) {
					t.Fatalf("got memory limit %d, expected %d", l, newMemorySize)
				}
			}
		})
	}
}

func Test_Container_UpdateResources_Annotation_Failure(t *testing.T) {
	requireFeatures(t, featureCRIUpdateContainer, featureCRIPlugin)

	// don't have an invalid value for Linux (container) resources similar to how Windows has
	tests := []struct {
		name           string
		features       []string
		runtimeHandler string
		podImage       string
		image          string
		cmd            []string
	}{
		{
			name:           "WCOW_Process",
			features:       []string{featureWCOWProcess},
			runtimeHandler: wcowProcessRuntimeHandler,
			podImage:       imageWindowsNanoserver,
			image:          imageWindowsNanoserver,
			cmd:            []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
		},
		{
			name:           "WCOW_Hypervisor",
			features:       []string{featureWCOWHypervisor},
			runtimeHandler: wcowHypervisorRuntimeHandler,
			podImage:       imageWindowsNanoserver,
			image:          imageWindowsNanoserver,
			cmd:            []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireFeatures(t, tt.features...)

			if tt.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLCOWImages(t, []string{tt.podImage})
			} else if tt.runtimeHandler == wcowHypervisorRuntimeHandler {
				pullRequiredImages(t, []string{tt.podImage})
			}

			podRequest := getRunPodSandboxRequest(t, tt.runtimeHandler)

			client := newTestRuntimeClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			podID := runPodSandbox(t, client, ctx, podRequest)
			defer removePodSandbox(t, client, ctx, podID)
			defer stopPodSandbox(t, client, ctx, podID)

			weight := uint32(5000)
			weightStr := strconv.FormatUint(uint64(weight), 10)
			containerRequest := &runtime.CreateContainerRequest{
				Config: &runtime.ContainerConfig{
					Metadata: &runtime.ContainerMetadata{
						Name: t.Name() + "-Container",
					},
					Image: &runtime.ImageSpec{
						Image: tt.image,
					},
					Command: tt.cmd,
					Annotations: map[string]string{
						annotations.ContainerProcessorWeight: weightStr,
					},
				},
				PodSandboxId:  podID,
				SandboxConfig: podRequest.Config,
			}

			containerID := createContainer(t, client, ctx, containerRequest)
			defer removeContainer(t, client, ctx, containerID)

			startContainer(t, client, ctx, containerID)
			defer stopContainer(t, client, ctx, containerID)

			updateReq := &runtime.UpdateContainerResourcesRequest{
				ContainerId: containerID,
				Windows:     &runtime.WindowsContainerResources{},
				Annotations: map[string]string{
					annotations.ContainerProcessorWeight: "10001",
				},
			}

			if _, err := client.UpdateContainerResources(ctx, updateReq); err == nil {
				t.Fatalf("updating container resources for %s should have failed", podID)
			}

			targetShimName := "k8s.io-" + podID

			jobExpectedValue := calculateJobCPUWeight(weight)
			checkWCOWResourceLimit(t, ctx, tt.runtimeHandler, targetShimName, containerID, "cpu-weight", uint64(jobExpectedValue))

			spec := getContainerOCISpec(t, client, ctx, containerID)
			checkAnnotation(t, spec, annotations.ContainerProcessorWeight, weightStr)
		})
	}

}
