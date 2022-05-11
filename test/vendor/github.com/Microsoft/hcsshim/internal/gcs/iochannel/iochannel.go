package iochannel

import (
	"net"
)

type Channel struct {
	l   net.Listener
	c   net.Conn
	err error
	ch  chan struct{}
}

func NewIoChannel(l net.Listener) *Channel {
	c := &Channel{
		l:  l,
		ch: make(chan struct{}),
	}
	go c.Accept()
	return c
}

func (c *Channel) Accept() {
	c.c, c.err = c.l.Accept()
	c.l.Close()
	close(c.ch)
}

func (c *Channel) Close() error {
	if c == nil {
		return nil
	}
	c.l.Close()
	<-c.ch
	if c.c != nil {
		c.c.Close()
	}
	return nil
}

type CloseWriter interface {
	CloseWrite() error
}

func (c *Channel) CloseWrite() error {
	<-c.ch
	if c.c == nil {
		return c.err
	}
	return c.c.(CloseWriter).CloseWrite()
}

func (c *Channel) Read(b []byte) (int, error) {
	<-c.ch
	if c.c == nil {
		return 0, c.err
	}
	return c.c.Read(b)
}

func (c *Channel) Write(b []byte) (int, error) {
	<-c.ch
	if c.c == nil {
		return 0, c.err
	}
	return c.c.Write(b)
}
