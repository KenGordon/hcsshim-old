package oci

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Microsoft/hcsshim/pkg/annotations"
	runhcsopts "github.com/Microsoft/hcsshim/pkg/service/options"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
)

func Test_SpecUpdate_ProcessorCount_NoAnnotation_WithOpts(t *testing.T) {
	opts := &runhcsopts.Options{
		VmProcessorCount: 4,
	}
	s := &specs.Spec{
		Linux:       &specs.Linux{},
		Annotations: map[string]string{},
	}
	updatedSpec := UpdateSpecFromOptions(*s, opts)

	if updatedSpec.Annotations[annotations.ProcessorCount] != "4" {
		t.Fatal("should have updated annotation to default when annotation is not provided in the spec")
	}
}

func Test_ProccessAnnotations_Expansion(t *testing.T) {
	// suppress warnings raised by process annotation
	defer func(l logrus.Level) {
		logrus.SetLevel(l)
	}(logrus.GetLevel())
	logrus.SetLevel(logrus.ErrorLevel)
	ctx := context.Background()

	tests := []struct {
		name string
		spec specs.Spec
	}{
		{
			name: "lcow",
			spec: specs.Spec{
				Linux: &specs.Linux{},
			},
		},
		{
			name: "wcow-hypervisor",
			spec: specs.Spec{
				Windows: &specs.Windows{
					HyperV: &specs.WindowsHyperV{},
				},
			},
		},
		{
			name: "wcow-process",
			spec: specs.Spec{
				Windows: &specs.Windows{},
			},
		},
	}

	for _, tt := range tests {
		// test correct expansion
		for _, v := range []string{"true", "false"} {
			t.Run(tt.name+"_disable_unsafe_"+v, func(subtest *testing.T) {
				tt.spec.Annotations = map[string]string{
					annotations.DisableUnsafeOperations: v,
				}

				err := ProcessAnnotations(ctx, &tt.spec)
				if err != nil {
					subtest.Fatalf("could not update spec from options: %v", err)
				}

				for _, k := range annotations.AnnotationExpansions[annotations.DisableUnsafeOperations] {
					if vv := tt.spec.Annotations[k]; vv != v {
						subtest.Fatalf("annotation %q was incorrectly expanded to %q, expected %q", k, vv, v)
					}
				}
			})
		}

		// test errors raised on conflict
		t.Run(tt.name+"_disable_unsafe_error", func(subtest *testing.T) {
			tt.spec.Annotations = map[string]string{
				annotations.DisableUnsafeOperations:   "true",
				annotations.DisableWritableFileShares: "false",
			}

			errExp := fmt.Sprintf("could not expand %q into %q",
				annotations.DisableUnsafeOperations,
				annotations.DisableWritableFileShares)

			err := ProcessAnnotations(ctx, &tt.spec)
			if !errors.Is(err, ErrAnnotationExpansionConflict) {
				t.Fatalf("UpdateSpecFromOptions should have failed with %q, actual was %v", errExp, err)
			}
		})
	}
}

func Test_SpecUpdate_MemorySize_WithAnnotation_WithOpts(t *testing.T) {
	opts := &runhcsopts.Options{
		VmMemorySizeInMb: 3072,
	}
	s := &specs.Spec{
		Linux: &specs.Linux{},
		Annotations: map[string]string{
			annotations.MemorySizeInMB: "2048",
		},
	}
	updatedSpec := UpdateSpecFromOptions(*s, opts)

	if updatedSpec.Annotations[annotations.MemorySizeInMB] != "2048" {
		t.Fatal("should not have updated annotation to default when annotation is provided in the spec")
	}
}

func Test_SpecUpdate_MemorySize_NoAnnotation_WithOpts(t *testing.T) {
	opts := &runhcsopts.Options{
		VmMemorySizeInMb: 3072,
	}
	s := &specs.Spec{
		Linux:       &specs.Linux{},
		Annotations: map[string]string{},
	}
	updatedSpec := UpdateSpecFromOptions(*s, opts)

	if updatedSpec.Annotations[annotations.MemorySizeInMB] != "3072" {
		t.Fatal("should have updated annotation to default when annotation is not provided in the spec")
	}
}

func Test_SpecUpdate_ProcessorCount_WithAnnotation_WithOpts(t *testing.T) {
	opts := &runhcsopts.Options{
		VmProcessorCount: 4,
	}
	s := &specs.Spec{
		Linux: &specs.Linux{},
		Annotations: map[string]string{
			annotations.ProcessorCount: "8",
		},
	}
	updatedSpec := UpdateSpecFromOptions(*s, opts)

	if updatedSpec.Annotations[annotations.ProcessorCount] != "8" {
		t.Fatal("should not have updated annotation to default when annotation is provided in the spec")
	}
}
