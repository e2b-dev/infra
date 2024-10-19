package sandbox

import (
	"context"
	"fmt"
	"net"
	"os/exec"
)

func getInstanceIP(instanceID string) (string, error) {
	server := "127.0.0.4"

	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "udp", server+":53")
		},
	}

	ips, err := r.LookupHost(context.Background(), instanceID)
	if err != nil {
		return "", fmt.Errorf("error looking up %s: %v", instanceID, err)
	}

	return ips[0], nil
}

func RunSSHCommand(instanceID, sshCmdStr string) (string, error) {
	ip, err := getInstanceIP(instanceID)
	if err != nil {
		return "", err
	}

	// Construct the SSH command with the "ls -al" command
	sshCmd := exec.Command(
		"ssh", fmt.Sprintf("root@%s", ip), "-o StrictHostKeyChecking=no", sshCmdStr)

	// Execute the SSH command
	output, err := sshCmd.CombinedOutput()
	if err != nil {
		return "", err
	}

	return string(output), nil
}

const (
	installCmd = "sudo apt -y install sysbench"
	stressCmd  = "sysbench memory --memory-block-size=128M --memory-total-size=1600M --memory-oper=write --verbosity=0 run"
)

func Stress(instanceID string) (string, error) {
	fmt.Printf("Installing sysbench on instance %s\n", instanceID)
	installOutput, err := RunSSHCommand(instanceID, installCmd)
	if err != nil {
		return installOutput, err
	}

	// Run sysbench
	fmt.Printf("Stressing instance %s\n$ %s\n", instanceID, stressCmd)
	return RunSSHCommand(instanceID, stressCmd)
}

func KillAllFirecrackerProcesses() error {
	cmd := exec.Command("sh", "-c", "pgrep -f firecracker | sudo xargs -r kill -9")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to cleanup Firecracker processes: %v, output: %s", err, string(output))
	}
	return nil
}
