package units

// MBShift is the bit-shift distance between bytes and megabytes.
// Use with << to convert MB to bytes, or >> to convert bytes to MB.
const MBShift = 20

// MBToBytes converts megabytes to bytes.
func MBToBytes(mb int64) int64 {
	return mb << MBShift
}

// BytesToMB converts bytes to megabytes.
func BytesToMB(b int64) int64 {
	return b >> MBShift
}
