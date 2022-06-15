package service

import (
	"github.com/Microsoft/hcsshim/internal/shim"
	"github.com/containerd/containerd/events"
)

type Options struct {
	Events                events.Publisher
	TID                   string
	IsSandbox             bool
	PodFactory            shim.PodFactory
	StandaloneTaskFactory shim.TaskFactory
}

type Option func(*Options)

func WithEventPublisher(e events.Publisher) Option {
	return func(o *Options) {
		o.Events = e
	}
}
func WithTID(tid string) Option {
	return func(o *Options) {
		o.TID = tid
	}
}
func WithIsSandbox(s bool) Option {
	return func(o *Options) {
		o.IsSandbox = s
	}
}

func WithPodFactory(pf shim.PodFactory) Option {
	return func(o *Options) {
		o.PodFactory = pf
	}
}

func WithStandaloneTaskFactory(sf shim.TaskFactory) Option {
	return func(o *Options) {
		o.StandaloneTaskFactory = sf
	}
}
