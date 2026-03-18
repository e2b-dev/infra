package constants

const (
	ServiceNameTemplate = "template-manager"

	// MBShift is the bit-shift distance between bytes and megabytes.
	// Use with << to convert MB to bytes, or >> to convert bytes to MB.
	MBShift = 20

	SystemdInitPath = "/sbin/init"
)
