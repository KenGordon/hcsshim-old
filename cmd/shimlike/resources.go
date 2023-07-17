package main

import (
	"context"

	p "github.com/Microsoft/hcsshim/cmd/shimlike/proto"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
	"github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// marshalLinuxResources marshals a LinuxContainerResources into an OCI spec
func marshalLinuxResources(c *p.LinuxContainerResources) *specs.LinuxResources {
	resources := &specs.LinuxResources{}
	resources.CPU = &specs.LinuxCPU{
		Cpus: c.CpusetCpus,
		Mems: c.CpusetMems,
	}
	if shares := uint64(c.CpuShares); shares != 0 {
		resources.CPU.Shares = &shares
	}
	if quota := int64(c.CpuQuota); quota != 0 {
		resources.CPU.Quota = &quota
	}
	if period := uint64(c.CpuPeriod); period != 0 {
		resources.CPU.Period = &period
	}

	if memory := int64(c.MemoryLimitInBytes); memory != 0 {
		resources.Memory = &specs.LinuxMemory{
			Limit: &memory,
		}
	}
	return resources
}

// updateContainerResources updates the resources of a container
func (s *RuntimeServer) updateContainerResources(ctx context.Context, containerID string, resources *p.LinuxContainerResources, annotations map[string]string) error {
	c := s.containers[containerID]
	if c == nil {
		return status.Error(codes.NotFound, "container not found")
	}
	if resources != nil {
		res := marshalLinuxResources(resources)
		settings := guestresource.LCOWContainerConstraints{
			Linux: *res,
		}
		modification := guestrequest.ModificationRequest{
			ResourceType: guestresource.ResourceTypeContainerConstraints,
			RequestType:  guestrequest.RequestTypeUpdate,
			Settings:     settings,
		}
		if err := c.ProcessHost.Modify(ctx, modification); err != nil {
			return err
		}
	}
	return nil
}
