//go:build !windows
// +build !windows

package oci

import (
	"github.com/containerd/containerd/api/types"
	"github.com/opencontainers/runtime-spec/specs-go"
)

// TODO katiewasnothere: figure out a better way to do this
func UpdateSpecWithLayerPaths(spec *specs.Spec, rootfs []*types.Mount) error {
	return nil
}
