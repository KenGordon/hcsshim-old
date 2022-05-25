package remotevm

import (
	"context"
	"errors"

	"github.com/Microsoft/hcsshim/internal/vm"
)

func WithIgnoreSupported() vm.CreateOpt {
	return func(ctx context.Context, uvmb vm.UVMBuilder) error {
		builder, ok := uvmb.(*utilityVMBuilder)
		if !ok {
			return errors.New("object is not a remotevm UVMBuilder")
		}
		builder.ignoreSupported = true
		return nil
	}
}

func WithNetWorkNamespace(ns string) vm.CreateOpt {
	return func(ctx context.Context, uvmb vm.UVMBuilder) error {
		builder, ok := uvmb.(*utilityVMBuilder)
		if !ok {
			return errors.New("object is not a remotevm UVMBuilder")
		}
		builder.networkNS = ns
		return nil
	}
}
