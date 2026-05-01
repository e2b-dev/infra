package userfaultfd

// Typed client wrapper around the parent ↔ child JSON-RPC channel.
// Hides the method-name strings, the Empty placeholder pointers, and
// the wire-vs-domain type translation from the testHandler and from
// the call sites in individual tests.

import (
	"io"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os/exec"
)

type harnessClient struct {
	rpc  *rpc.Client
	conn io.Closer
	// cmd is retained so a future cleanup or diagnostic can reach
	// the underlying process; today the test cleanup in the parent
	// drives Wait directly.
	cmd *exec.Cmd
}

func newHarnessClient(conn net.Conn, cmd *exec.Cmd) *harnessClient {
	return &harnessClient{
		rpc:  jsonrpc.NewClient(conn),
		conn: conn,
		cmd:  cmd,
	}
}

func (c *harnessClient) Bootstrap(args BootstrapArgs) error {
	return c.rpc.Call("Lifecycle.Bootstrap", &args, &BootstrapReply{})
}

func (c *harnessClient) WaitReady() error {
	return c.rpc.Call("Lifecycle.WaitReady", &Empty{}, &Empty{})
}

func (c *harnessClient) Shutdown() error {
	return c.rpc.Call("Lifecycle.Shutdown", &Empty{}, &Empty{})
}

func (c *harnessClient) Pause() error {
	return c.rpc.Call("Paging.Pause", &Empty{}, &Empty{})
}

func (c *harnessClient) Resume() error {
	return c.rpc.Call("Paging.Resume", &Empty{}, &Empty{})
}

func (c *harnessClient) PageStates() ([]pageStateEntry, error) {
	var reply PageStatesReply
	if err := c.rpc.Call("Paging.States", &Empty{}, &reply); err != nil {
		return nil, err
	}

	return reply.Entries, nil
}

func (c *harnessClient) InstallBarrier(addr uint64, point barrierPoint) (uint64, error) {
	var reply FaultBarrierReply
	if err := c.rpc.Call("Barriers.Install", &FaultBarrierArgs{Addr: addr, Point: uint8(point)}, &reply); err != nil {
		return 0, err
	}

	return reply.Token, nil
}

func (c *harnessClient) WaitFaultHeld(token uint64) error {
	return c.rpc.Call("Barriers.WaitHeld", &TokenArgs{Token: token}, &Empty{})
}

func (c *harnessClient) ReleaseFault(token uint64) error {
	return c.rpc.Call("Barriers.Release", &TokenArgs{Token: token}, &Empty{})
}

func (c *harnessClient) Close() error {
	return c.conn.Close()
}
