package testharness

import (
	"io"
	"net/rpc"
	"net/rpc/jsonrpc"
)

// Client is the typed parent-side wrapper around the JSON-RPC channel
// to the child helper process.
type Client struct {
	rpc  *rpc.Client
	conn io.Closer
}

// NewClient wraps an already-connected duplex stream. Closing the
// returned Client closes the underlying conn.
func NewClient(conn io.ReadWriteCloser) *Client {
	return &Client{
		rpc:  jsonrpc.NewClient(conn),
		conn: conn,
	}
}

func (c *Client) Bootstrap(args BootstrapArgs) error {
	return c.rpc.Call("Lifecycle.Bootstrap", &args, &BootstrapReply{})
}

func (c *Client) WaitReady() error {
	return c.rpc.Call("Lifecycle.WaitReady", &Empty{}, &Empty{})
}

func (c *Client) Shutdown() error {
	return c.rpc.Call("Lifecycle.Shutdown", &Empty{}, &Empty{})
}

func (c *Client) Pause() error {
	return c.rpc.Call("Paging.Pause", &Empty{}, &Empty{})
}

func (c *Client) Resume() error {
	return c.rpc.Call("Paging.Resume", &Empty{}, &Empty{})
}

func (c *Client) PageStates() ([]PageStateEntry, error) {
	var reply PageStatesReply
	if err := c.rpc.Call("Paging.States", &Empty{}, &reply); err != nil {
		return nil, err
	}

	return reply.Entries, nil
}

func (c *Client) InstallBarrier(addr uintptr, point Point) (uint64, error) {
	var reply FaultBarrierReply
	if err := c.rpc.Call("Barriers.Install", &FaultBarrierArgs{Addr: uint64(addr), Point: uint8(point)}, &reply); err != nil {
		return 0, err
	}

	return reply.Token, nil
}

func (c *Client) WaitFaultHeld(token uint64) error {
	return c.rpc.Call("Barriers.WaitHeld", &TokenArgs{Token: token}, &Empty{})
}

func (c *Client) ReleaseFault(token uint64) error {
	return c.rpc.Call("Barriers.Release", &TokenArgs{Token: token}, &Empty{})
}

func (c *Client) Close() error {
	return c.conn.Close()
}
