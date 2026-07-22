//go:build linux

package rootfs

import (
	"encoding/binary"
	"testing"
)

// makeSuperblock builds a 1024-byte ext superblock with the given magic and
// incompat-feature fields set at their real offsets.
func makeSuperblock(magic uint16, incompat uint32) []byte {
	sb := make([]byte, ext4SuperblockSize)
	binary.LittleEndian.PutUint16(sb[ext4MagicOffset:], magic)
	binary.LittleEndian.PutUint32(sb[ext4FeatureIncompatOffset:], incompat)

	return sb
}

func TestParseExt4Superblock(t *testing.T) {
	t.Parallel()

	// Typical incompat features on an ext4 rootfs (extent|64bit|flex_bg), without
	// needs_recovery. This is also what a cleanly-frozen snapshot looks like:
	// ext4_freeze clears needs_recovery even though the fs stays mounted.
	const cleanIncompat = 0x0200 | 0x0080 | 0x0040

	tests := []struct {
		name         string
		sb           []byte
		wantExt      bool
		wantRecovery bool
	}{
		{
			name:         "clean / frozen snapshot (no needs_recovery)",
			sb:           makeSuperblock(ext4Magic, cleanIncompat),
			wantExt:      true,
			wantRecovery: false,
		},
		{
			name:         "needs_recovery feature set (torn journal)",
			sb:           makeSuperblock(ext4Magic, cleanIncompat|ext4FeatureIncompatRecover),
			wantExt:      true,
			wantRecovery: true,
		},
		{
			name:         "not an ext filesystem (bad magic)",
			sb:           makeSuperblock(0x1234, cleanIncompat),
			wantExt:      false,
			wantRecovery: false,
		},
		{
			name:         "short buffer",
			sb:           make([]byte, 8),
			wantExt:      false,
			wantRecovery: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			isExt, needsRecovery := parseExt4Superblock(tt.sb)
			if isExt != tt.wantExt || needsRecovery != tt.wantRecovery {
				t.Fatalf("parseExt4Superblock() = (ext=%v, recovery=%v), want (ext=%v, recovery=%v)",
					isExt, needsRecovery, tt.wantExt, tt.wantRecovery)
			}
		})
	}
}

func TestE2fsckExitOK(t *testing.T) {
	t.Parallel()

	// e2fsck exit codes are a summing bitmask.
	oks := []int{0, 1, 2, 3}         // clean / corrected / reboot / corrected+reboot
	fails := []int{4, 5, 6, 7, 8, 9} // any bit >= 0x04 (uncorrected / operational / ...)

	for _, code := range oks {
		if !e2fsckExitOK(code) {
			t.Errorf("e2fsckExitOK(%d) = false, want true", code)
		}
	}
	for _, code := range fails {
		if e2fsckExitOK(code) {
			t.Errorf("e2fsckExitOK(%d) = true, want false", code)
		}
	}
}
