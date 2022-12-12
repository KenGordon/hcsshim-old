//go:build windows && functional
// +build windows,functional

package cri_containerd

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/Microsoft/hcsshim/internal/cpugroup"
	"github.com/Microsoft/hcsshim/internal/memory"
	"github.com/Microsoft/hcsshim/internal/processorinfo"
	"github.com/Microsoft/hcsshim/pkg/annotations"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

func Test_Pod_UpdateResources_Memory(t *testing.T) {
	type config struct {
		name             string
		requiredFeatures []string
		runtimeHandler   string
		sandboxImage     string
	}
	tests := []config{
		{
			name:             "WCOW_Hypervisor",
			requiredFeatures: []string{featureWCOWHypervisor},
			runtimeHandler:   wcowHypervisorRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
		},
		{
			name:             "LCOW",
			requiredFeatures: []string{featureLCOW},
			runtimeHandler:   lcowRuntimeHandler,
			sandboxImage:     imageLcowK8sPause,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireFeatures(t, test.requiredFeatures...)

			if test.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLCOWImages(t, []string{test.sandboxImage})
			} else {
				pullRequiredImages(t, []string{test.sandboxImage})
			}
			var startingMemorySize int64 = 768 * memory.MiB
			podRequest := getRunPodSandboxRequest(
				t,
				test.runtimeHandler,
				WithSandboxAnnotations(map[string]string{
					annotations.ContainerMemorySizeInMB: fmt.Sprintf("%d", startingMemorySize),
				}),
			)

			client := newTestRuntimeClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			podID := runPodSandbox(t, client, ctx, podRequest)
			defer removePodSandbox(t, client, ctx, podID)
			defer stopPodSandbox(t, client, ctx, podID)

			// make request for shrinking memory size
			newMemorySize := startingMemorySize / 2
			updateReq := &runtime.UpdateContainerResourcesRequest{
				ContainerId: podID,
			}

			if test.runtimeHandler == lcowRuntimeHandler {
				updateReq.Linux = &runtime.LinuxContainerResources{
					MemoryLimitInBytes: newMemorySize,
				}
			} else {
				updateReq.Windows = &runtime.WindowsContainerResources{
					MemoryLimitInBytes: newMemorySize,
				}
			}

			if _, err := client.UpdateContainerResources(ctx, updateReq); err != nil {
				t.Fatalf("updating container resources for %s with %v", podID, err)
			}
		})
	}
}

func Test_Pod_UpdateResources_Memory_PA(t *testing.T) {
	type config struct {
		name             string
		requiredFeatures []string
		runtimeHandler   string
		sandboxImage     string
	}
	tests := []config{
		{
			name:             "WCOW_Hypervisor",
			requiredFeatures: []string{featureWCOWHypervisor},
			runtimeHandler:   wcowHypervisorRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
		},
		{
			name:             "LCOW",
			requiredFeatures: []string{featureLCOW},
			runtimeHandler:   lcowRuntimeHandler,
			sandboxImage:     imageLcowK8sPause,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireFeatures(t, test.requiredFeatures...)

			if test.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLCOWImages(t, []string{test.sandboxImage})
			} else {
				pullRequiredImages(t, []string{test.sandboxImage})
			}
			var startingMemorySize int64 = 200 * memory.MiB
			podRequest := getRunPodSandboxRequest(
				t,
				test.runtimeHandler,
				WithSandboxAnnotations(map[string]string{
					annotations.FullyPhysicallyBacked:   "true",
					annotations.ContainerMemorySizeInMB: fmt.Sprintf("%d", startingMemorySize),
				}),
			)

			client := newTestRuntimeClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			podID := runPodSandbox(t, client, ctx, podRequest)
			defer removePodSandbox(t, client, ctx, podID)
			defer stopPodSandbox(t, client, ctx, podID)

			// make request for shrinking memory size
			newMemorySize := startingMemorySize / 2
			updateReq := &runtime.UpdateContainerResourcesRequest{
				ContainerId: podID,
			}

			if test.runtimeHandler == lcowRuntimeHandler {
				updateReq.Linux = &runtime.LinuxContainerResources{
					MemoryLimitInBytes: newMemorySize,
				}
			} else {
				updateReq.Windows = &runtime.WindowsContainerResources{
					MemoryLimitInBytes: newMemorySize,
				}
			}

			if _, err := client.UpdateContainerResources(ctx, updateReq); err != nil {
				t.Fatalf("updating container resources for %s with %v", podID, err)
			}
		})
	}
}

func Test_Pod_UpdateResources_CPUShares(t *testing.T) {
	type config struct {
		name             string
		requiredFeatures []string
		runtimeHandler   string
		sandboxImage     string
	}
	tests := []config{
		{
			name:             "WCOW_Hypervisor",
			requiredFeatures: []string{featureWCOWHypervisor},
			runtimeHandler:   wcowHypervisorRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
		},
		{
			name:             "LCOW",
			requiredFeatures: []string{featureLCOW},
			runtimeHandler:   lcowRuntimeHandler,
			sandboxImage:     imageLcowK8sPause,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireFeatures(t, test.requiredFeatures...)

			if test.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLCOWImages(t, []string{test.sandboxImage})
			} else {
				pullRequiredImages(t, []string{test.sandboxImage})
			}
			podRequest := getRunPodSandboxRequest(t, test.runtimeHandler)

			client := newTestRuntimeClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			podID := runPodSandbox(t, client, ctx, podRequest)
			defer removePodSandbox(t, client, ctx, podID)
			defer stopPodSandbox(t, client, ctx, podID)

			updateReq := &runtime.UpdateContainerResourcesRequest{
				ContainerId: podID,
			}

			if test.runtimeHandler == lcowRuntimeHandler {
				updateReq.Linux = &runtime.LinuxContainerResources{
					CpuShares: 2000,
				}
			} else {
				updateReq.Windows = &runtime.WindowsContainerResources{
					CpuShares: 2000,
				}
			}

			if _, err := client.UpdateContainerResources(ctx, updateReq); err != nil {
				t.Fatalf("updating container resources for %s with %v", podID, err)
			}
		})
	}
}

func Test_Pod_UpdateResources_CPUGroup(t *testing.T) {
	t.Skip("Skipping for now")
	ctx := context.Background()

	processorTopology, err := processorinfo.HostProcessorInfo(ctx)
	if err != nil {
		t.Fatalf("failed to get host processor information: %s", err)
	}
	lpIndices := make([]uint32, processorTopology.LogicalProcessorCount)
	for i, p := range processorTopology.LogicalProcessors {
		lpIndices[i] = p.LpIndex
	}

	startCPUGroupID := "FA22A12C-36B3-486D-A3E9-BC526C2B450B"
	if err := cpugroup.Create(ctx, startCPUGroupID, lpIndices); err != nil {
		t.Fatalf("failed to create test cpugroup with: %v", err)
	}

	defer func() {
		err := cpugroup.Delete(ctx, startCPUGroupID)
		if err != nil && err != cpugroup.ErrHVStatusInvalidCPUGroupState {
			t.Fatalf("failed to clean up test cpugroup with: %v", err)
		}
	}()

	updateCPUGroupID := "FA22A12C-36B3-486D-A3E9-BC526C2B450C"
	if err := cpugroup.Create(ctx, updateCPUGroupID, lpIndices); err != nil {
		t.Fatalf("failed to create test cpugroup with: %v", err)
	}

	defer func() {
		err := cpugroup.Delete(ctx, updateCPUGroupID)
		if err != nil && err != cpugroup.ErrHVStatusInvalidCPUGroupState {
			t.Fatalf("failed to clean up test cpugroup with: %v", err)
		}
	}()

	//nolint:unused // false positive about config being unused
	type config struct {
		name             string
		requiredFeatures []string
		runtimeHandler   string
		sandboxImage     string
	}

	tests := []config{
		{
			name:             "WCOW_Hypervisor",
			requiredFeatures: []string{featureWCOWHypervisor},
			runtimeHandler:   wcowHypervisorRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
		},
		{
			name:             "LCOW",
			requiredFeatures: []string{featureLCOW},
			runtimeHandler:   lcowRuntimeHandler,
			sandboxImage:     imageLcowK8sPause,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireFeatures(t, test.requiredFeatures...)
			if test.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLCOWImages(t, []string{test.sandboxImage})
			} else {
				pullRequiredImages(t, []string{test.sandboxImage})
			}

			podRequest := getRunPodSandboxRequest(t, test.runtimeHandler, WithSandboxAnnotations(map[string]string{
				annotations.CPUGroupID: startCPUGroupID,
			}))
			client := newTestRuntimeClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			podID := runPodSandbox(t, client, ctx, podRequest)
			defer removePodSandbox(t, client, ctx, podID)
			defer stopPodSandbox(t, client, ctx, podID)

			updateReq := &runtime.UpdateContainerResourcesRequest{
				ContainerId: podID,
				Annotations: map[string]string{
					annotations.CPUGroupID: updateCPUGroupID,
				},
			}

			if test.runtimeHandler == lcowRuntimeHandler {
				updateReq.Linux = &runtime.LinuxContainerResources{}
			} else {
				updateReq.Windows = &runtime.WindowsContainerResources{}
			}

			if _, err := client.UpdateContainerResources(ctx, updateReq); err != nil {
				t.Fatalf("updating container resources for %s with %v", podID, err)
			}
		})
	}
}

func Test_Pod_UpdateResources_Restart(t *testing.T) {
	requireFeatures(t, featureCRIUpdateContainer, featureCRIPlugin)

	enableReset := "io.microsoft.cri.enablereset"
	fakeAnnotation := "io.microsoft.virtualmachine.computetopology.fake-annotation"

	tests := []struct {
		name           string
		features       []string
		runtimeHandler string
		podImage       string
		image          string
		cmd            []string
		checkCmd       []string
		useAnnotation  bool
	}{
		{
			name:           "WCOW_Hypervisor",
			features:       []string{featureWCOWHypervisor},
			runtimeHandler: wcowHypervisorRuntimeHandler,
			podImage:       imageWindowsNanoserver,
			image:          imageWindowsNanoserver,
			cmd:            []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
			checkCmd:       []string{"cmd", "/c", `echo %NUMBER_OF_PROCESSORS%`},
		},
		{
			name:           "LCOW",
			features:       []string{featureLCOW},
			runtimeHandler: lcowRuntimeHandler,
			podImage:       imageLcowK8sPause,
			image:          imageLcowAlpine,
			cmd:            []string{"top"},
			checkCmd:       []string{"ash", "-c", "nproc"},
		},
	}
	for i := range tests {
		tt := tests[i]
		tt.useAnnotation = true
		tests = append(tests, tt)
	}

	for _, tt := range tests {
		n := tt.name
		if tt.useAnnotation {
			n += "_Annotation"
		}
		t.Run(n, func(t *testing.T) {
			requireFeatures(t, tt.features...)

			if tt.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLCOWImages(t, []string{tt.podImage})
			} else if tt.runtimeHandler == wcowHypervisorRuntimeHandler {
				pullRequiredImages(t, []string{tt.podImage})
			}

			client := newTestRuntimeClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			count := int64(3)
			// cannot specify pod count at start without annotations, but currently annotations
			// and resource limits do not mix, so have the pod start with default values
			if !tt.useAnnotation {
				// same as default; should just fine
				count = 2
			}
			countStr := strconv.FormatInt(count, 10)
			podAnnotations := map[string]string{
				enableReset: "true",
			}
			// annotations and resource settings do no mix
			if tt.useAnnotation {
				podAnnotations[annotations.ProcessorCount] = countStr
			}
			podRequest := getRunPodSandboxRequest(t, tt.runtimeHandler, WithSandboxAnnotations(podAnnotations))
			podID := runPodSandbox(t, client, ctx, podRequest)
			defer removePodSandbox(t, client, ctx, podID)
			defer stopPodSandbox(t, client, ctx, podID)

			cRequest := getCreateContainerRequest(podID, t.Name()+"-Container", tt.image, tt.cmd, podRequest.Config)
			cRequest.Config.Annotations = map[string]string{
				enableReset: "true",
			}

			cID := createContainer(t, client, ctx, cRequest)
			startContainer(t, client, ctx, cID)
			defer removeContainer(t, client, ctx, cID)
			defer stopContainer(t, client, ctx, cID)

			execRequest := &runtime.ExecSyncRequest{
				ContainerId: cID,
				Cmd:         tt.checkCmd,
				Timeout:     1,
			}
			execSuccess(t, client, ctx, execRequest, countStr)

			newCount := int64(1)
			newCountStr := strconv.FormatInt(newCount, 10)
			// updating the pod on the windows side, so use WindowsContainerResources
			req := &runtime.UpdateContainerResourcesRequest{
				ContainerId: podID,
				Windows:     &runtime.WindowsContainerResources{},
				Annotations: map[string]string{
					fakeAnnotation: "this shouldn't persist",
				},
			}

			if tt.useAnnotation {
				req.Annotations[annotations.ProcessorCount] = newCountStr
			} else {
				req.Windows.CpuCount = newCount
			}

			updateContainer(t, client, ctx, req)
			t.Logf("update request for pod with %+v and annotations: %+v", req.Windows, req.Annotations)

			spec := getPodSandboxOCISpec(t, client, ctx, podID)
			checkAnnotation(t, spec, fakeAnnotation, "")
			if tt.useAnnotation {
				checkAnnotation(t, spec, annotations.ProcessorCount, newCountStr)
			} else {
				if v := *spec.Windows.Resources.CPU.Count; v != uint64(newCount) {
					t.Fatalf("got %d CPU cores, expected %d", v, newCount)
				}
			}

			// check persistance after update

			stopContainer(t, client, ctx, cID)
			stopPodSandbox(t, client, ctx, podID)

			// let GC run and things be cleaned up
			time.Sleep(500 * time.Millisecond)

			runPodSandbox(t, client, ctx, podRequest)
			startContainer(t, client, ctx, cID)

			execSuccess(t, client, ctx, execRequest, newCountStr)

			// spec updates should persist
			spec = getPodSandboxOCISpec(t, client, ctx, podID)
			if tt.useAnnotation {
				checkAnnotation(t, spec, annotations.ProcessorCount, newCountStr)
			} else {
				if v := *spec.Windows.Resources.CPU.Count; v != uint64(newCount) {
					t.Fatalf("got %d CPU cores, expected %d", v, newCount)
				}
			}
		})
	}
}
