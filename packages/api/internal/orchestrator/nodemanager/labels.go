package nodemanager

func (n *Node) setLabels(labels []string) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	labelsSet := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		labelsSet[l] = struct{}{}
	}

	// todo: remove this once orchestrator nodes all have labels (default or otherwise)
	if len(labelsSet) == 0 {
		labelsSet["default"] = struct{}{}
	}

	n.labels = labelsSet
}

func (n *Node) Labels() map[string]struct{} {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return n.labels
}
