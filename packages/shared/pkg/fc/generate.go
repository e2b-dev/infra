package fc

// Wipe previously generated output so renamed/removed spec entities don't leave
// orphan files behind (go-swagger generates additively and never prunes).
//go:generate rm -rf client models
//go:generate go tool swagger generate client -f firecracker.yml -A firecracker
