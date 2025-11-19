package nodemanager

type MachineInfo struct {
	CPUFamily       string
	CPUModel        string
	CPUArchitecture string
}

func (n *Node) setMachineInfo(mi MachineInfo) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	n.machineInfo = mi
}

func (n *Node) MachineInfo() MachineInfo {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return n.machineInfo
}
