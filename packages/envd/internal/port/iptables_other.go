//go:build !linux

package port

import "context"

type iptablesBackend struct{}

func newIPtablesBackend(_ string) *iptablesBackend                      { return &iptablesBackend{} }
func setupIPv4DNAT() error                                              { return nil }
func (b *iptablesBackend) addRule(_ context.Context, _ uint32) error    { return nil }
func (b *iptablesBackend) deleteRule(_ context.Context, _ uint32) error { return nil }
