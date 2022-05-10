package service

import "github.com/containerd/containerd/events"

type Options struct {
	Events    events.Publisher
	TID       string
	IsSandbox bool
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
