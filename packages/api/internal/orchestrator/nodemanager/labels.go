package nodemanager

func (n *Node) setLabels(labels []string) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	n.labels = labels
}

func (n *Node) Labels() []string {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return n.labels
}
