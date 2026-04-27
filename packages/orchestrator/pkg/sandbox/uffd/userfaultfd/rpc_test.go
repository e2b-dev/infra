package userfaultfd

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// rpc_test.go is a tiny request/response RPC harness for the
// userfaultfd cross-process tests. Before this layer existed the
// parent ↔ child wire was a pile of single-purpose pipes (offsets,
// ready, gate-cmd, gate-sync) plus a SIGUSR2 trigger; every new
// piece of test-only inspection or coordination meant adding another
// fd and another env var. This file replaces all of that with a
// single bidirectional channel:
//
//   - Wire format: 4-byte big-endian length prefix, JSON body.
//   - Envelope: { ID, Method, Params } / { ID, Result, Error }.
//   - Multiple in-flight requests are correlated by ID, so an RPC
//     handler is free to block (e.g. WaitFaultHeld) while the parent
//     continues issuing other RPCs.
//
// Two pipes are used: rpc-req (parent → child, fd 5 in the child)
// and rpc-resp (child → parent, fd 6 in the child). The other test-
// only fds (uffd dup, content) are unchanged.

// rpcRequest is the on-wire request envelope.
type rpcRequest struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is the on-wire response envelope. Exactly one of Result
// or Error is set per response.
type rpcResponse struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// rpcError is the parent-side error type returned when a server
// handler reports a non-nil error. Tests can errors.Is / errors.As
// against this if they need to match a specific RPC failure.
type rpcError struct {
	Method string
	Msg    string
}

func (e *rpcError) Error() string {
	if e.Method == "" {
		return e.Msg
	}

	return fmt.Sprintf("rpc %s: %s", e.Method, e.Msg)
}

// writeFrame writes a length-prefixed JSON frame to w. Caller must
// hold any write mutex if w is shared.
func writeFrame(w io.Writer, body []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}

	return nil
}

// readFrame reads a single length-prefixed JSON frame from r. Returns
// io.EOF cleanly when the peer closes the pipe.
func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}

	n := binary.BigEndian.Uint32(hdr[:])
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}

	return body, nil
}

// rpcClient is the parent side of the RPC. Call is safe to invoke
// concurrently from any number of goroutines.
type rpcClient struct {
	w io.Writer

	writeMu sync.Mutex
	nextID  atomic.Uint64

	mu        sync.Mutex
	pending   map[uint64]chan rpcResponse
	closed    bool
	readerErr error
}

// newRPCClient starts the response reader goroutine. It does not own
// the lifecycle of r/w; the caller must close them.
func newRPCClient(r io.Reader, w io.Writer) *rpcClient {
	c := &rpcClient{
		w:       w,
		pending: make(map[uint64]chan rpcResponse),
	}

	go c.readLoop(r)

	return c
}

func (c *rpcClient) readLoop(r io.Reader) {
	for {
		body, err := readFrame(r)
		if err != nil {
			c.mu.Lock()
			c.closed = true
			c.readerErr = err
			pending := c.pending
			c.pending = nil
			c.mu.Unlock()

			for _, ch := range pending {
				select {
				case ch <- rpcResponse{Error: fmt.Sprintf("rpc client closed: %v", err)}:
				default:
				}
				close(ch)
			}

			return
		}

		var resp rpcResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			// Malformed frame from the child — drop it but keep going;
			// the test will time out on the affected Call rather than
			// the whole client wedging silently.
			continue
		}

		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()

		if !ok {
			continue
		}

		ch <- resp
		close(ch)
	}
}

// Call invokes a remote method. params is JSON-marshaled (pass nil for
// no params). result, if non-nil, is JSON-unmarshaled from the reply's
// Result field. Errors:
//
//   - context cancellation returns the context error
//   - a non-empty Error field on the response returns *rpcError
//   - if the read loop has died, returns the underlying read error
func (c *rpcClient) Call(ctx context.Context, method string, params, result any) error {
	id := c.nextID.Add(1)

	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("rpc %s: marshal params: %w", method, err)
		}
		paramsRaw = b
	}

	body, err := json.Marshal(rpcRequest{ID: id, Method: method, Params: paramsRaw})
	if err != nil {
		return fmt.Errorf("rpc %s: marshal envelope: %w", method, err)
	}

	ch := make(chan rpcResponse, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()

		return fmt.Errorf("rpc %s: client closed: %w", method, c.readerErr)
	}
	c.pending[id] = ch
	c.mu.Unlock()

	c.writeMu.Lock()
	err = writeFrame(c.w, body)
	c.writeMu.Unlock()

	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()

		return fmt.Errorf("rpc %s: write: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()

		return ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return fmt.Errorf("rpc %s: client closed", method)
		}
		if resp.Error != "" {
			return &rpcError{Method: method, Msg: resp.Error}
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("rpc %s: unmarshal result: %w", method, err)
			}
		}

		return nil
	}
}

// rpcHandler is the function signature a method handler must satisfy.
// params is the raw JSON params from the request; the handler returns
// any value to JSON-marshal back as the result, plus an error.
type rpcHandler func(ctx context.Context, params json.RawMessage) (any, error)

// rpcServer is the child side. Each request is dispatched to its own
// goroutine so handlers may block (e.g. WaitFaultHeld) without
// stalling other RPCs.
type rpcServer struct {
	r io.Reader
	w io.Writer

	writeMu  sync.Mutex
	handlers map[string]rpcHandler

	wg sync.WaitGroup
}

func newRPCServer(r io.Reader, w io.Writer) *rpcServer {
	return &rpcServer{
		r:        r,
		w:        w,
		handlers: make(map[string]rpcHandler),
	}
}

// Register adds a handler for the given method. Must be called BEFORE
// Serve.
func (s *rpcServer) Register(method string, h rpcHandler) {
	s.handlers[method] = h
}

// Serve reads frames from r and dispatches them in goroutines until
// the reader returns EOF / error. Returns when the reader is done; it
// then waits for all in-flight handlers to finish.
func (s *rpcServer) Serve(ctx context.Context) error {
	for {
		body, err := readFrame(s.r)
		if err != nil {
			s.wg.Wait()
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}

			return err
		}

		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			s.send(rpcResponse{ID: 0, Error: fmt.Sprintf("malformed request: %v", err)})

			continue
		}

		handler, ok := s.handlers[req.Method]
		if !ok {
			s.send(rpcResponse{ID: req.ID, Error: fmt.Sprintf("unknown method %q", req.Method)})

			continue
		}

		s.wg.Add(1)
		go func(req rpcRequest, h rpcHandler) {
			defer s.wg.Done()

			result, err := h(ctx, req.Params)
			resp := rpcResponse{ID: req.ID}
			if err != nil {
				resp.Error = err.Error()
			} else if result != nil {
				b, err := json.Marshal(result)
				if err != nil {
					resp.Error = fmt.Sprintf("marshal result: %v", err)
				} else {
					resp.Result = b
				}
			}

			s.send(resp)
		}(req, handler)
	}
}

func (s *rpcServer) send(resp rpcResponse) {
	body, err := json.Marshal(resp)
	if err != nil {
		// Best-effort: respond with an error envelope using the empty body.
		body, _ = json.Marshal(rpcResponse{ID: resp.ID, Error: fmt.Sprintf("marshal response: %v", err)})
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = writeFrame(s.w, body)
}
