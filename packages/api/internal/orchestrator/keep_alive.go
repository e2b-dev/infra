package orchestrator

import "time"

func (o *Orchestrator) KeepAliveFor(sandboxID string, duration time.Duration, allowShorter bool) error {
	_, err := o.instanceCache.KeepAliveFor(sandboxID, duration, allowShorter)
	return err
}
