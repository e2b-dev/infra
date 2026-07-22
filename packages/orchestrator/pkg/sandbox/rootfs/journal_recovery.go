//go:build linux

package rootfs

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// ext4 superblock layout: the primary superblock lives at byte 1024 of the
// filesystem; these offsets are relative to its start.
const (
	ext4SuperblockOffset = 1024
	ext4SuperblockSize   = 1024

	ext4MagicOffset           = 0x38 // __le16
	ext4FeatureIncompatOffset = 0x60 // __le32

	ext4Magic                  = 0xEF53
	ext4FeatureIncompatRecover = 0x0004 // set when the journal needs replay
)

// JournalRecoveryTimeout bounds an e2fsck run. Callers pair it with a cancel-free
// context so a request cancellation (a pause or create timeout) can't kill e2fsck
// mid-write — which would persist a half-repaired filesystem — while still capping
// how long recovery may take so it can't consume the caller's whole budget.
const JournalRecoveryTimeout = 2 * time.Minute

// e2fsck exit codes are a bitmask that sums: these two bits mean the filesystem
// was made consistent. Any higher bit (0x04 = errors left uncorrected, 0x08 =
// operational error, ...) means it was not.
const (
	e2fsckErrorsCorrected   = 0x01 // errors corrected
	e2fsckRebootRecommended = 0x02 // errors corrected, reboot recommended
)

// RecoverExt4Journal makes an ext4 filesystem mountable when its journal was
// captured torn — as happens for a filesystem-only pause on a guest whose envd
// predates FIFREEZE support and therefore only sync'd — which otherwise fails
// the resume cold-boot with "JBD2: recovery failed" / "error loading journal".
//
// The check is cheap: it reads only the superblock (a block the boot reads
// anyway) and looks at two bits, so the common clean case exits without spawning
// anything. Only a genuinely-dirty ext4 filesystem pays the e2fsck. Returns
// recovered=false (no-op) for a clean filesystem, a non-ext device, or an
// unreadable superblock. The device must not be mounted or written concurrently.
//
// Why e2fsck rather than a host mount-and-replay: a plain mount invokes the same
// jbd2 recovery the guest kernel does, which aborts on the torn commit's bad
// checksum. e2fsck -y replays the journal too, but discards the torn, never-
// committed transaction at its bad checksum and repairs any resulting
// inconsistency — yielding a mountable filesystem. Discarding that transaction is
// the correct crash-recovery
// outcome (that data never reached the block device).
func RecoverExt4Journal(ctx context.Context, devicePath string) (recovered bool, e error) {
	ctx, span := tracer.Start(ctx, "ext4-journal-recovery", trace.WithAttributes(
		attribute.String("device", devicePath),
	))
	defer span.End()

	sb, err := readSuperblock(devicePath)
	if err != nil {
		// Can't read the superblock: don't touch a device we can't reason about,
		// leave it to the normal boot path.
		span.SetAttributes(attribute.String("skip_reason", "superblock-unreadable"))

		return false, nil
	}

	isExt, needsRecovery := parseExt4Superblock(sb)
	if !isExt {
		span.SetAttributes(attribute.String("skip_reason", "not-ext"))

		return false, nil
	}
	if !needsRecovery {
		span.SetAttributes(attribute.Bool("needs_recovery", false))

		return false, nil
	}
	span.SetAttributes(attribute.Bool("needs_recovery", true))
	logger.L().Info(ctx, "recovering torn ext4 journal (running e2fsck -y)", zap.String("device", devicePath))

	start := time.Now()
	err = runE2fsck(ctx, devicePath)
	elapsed := time.Since(start)
	span.SetAttributes(attribute.Int64("e2fsck_ms", elapsed.Milliseconds()))

	if err != nil {
		logger.L().Error(ctx, "ext4 journal recovery failed",
			zap.String("device", devicePath),
			zap.Duration("elapsed", elapsed),
			zap.Bool("ctx_cancelled", ctx.Err() != nil),
			zap.Error(err),
		)

		return false, fmt.Errorf("e2fsck %s (after %s): %w", devicePath, elapsed, err)
	}

	logger.L().Info(ctx, "ext4 journal recovery complete",
		zap.String("device", devicePath),
		zap.Duration("elapsed", elapsed),
	)

	return true, nil
}

// parseExt4Superblock reports whether sb (a filesystem's 1024-byte superblock,
// i.e. bytes [1024, 2048) of the device) is an ext filesystem and whether its
// journal must be replayed before it can be safely mounted.
//
// The signal is the EXT4_FEATURE_INCOMPAT_RECOVER feature — the kernel's own
// "replay the journal at mount" flag. A torn snapshot (captured with un-replayed
// journal transactions) has it set; ext4_freeze() flushes the journal and clears
// it, so a cleanly-frozen snapshot reads clean and is skipped. We deliberately do
// NOT treat a clear EXT4_VALID_FS ("cleanly unmounted") as dirty: that bit is
// clear on any snapshot of a still-mounted filesystem — i.e. every snapshot — so
// keying on it would force e2fsck on every reboot.
func parseExt4Superblock(sb []byte) (isExt bool, needsRecovery bool) {
	if len(sb) < ext4FeatureIncompatOffset+4 {
		return false, false
	}
	if binary.LittleEndian.Uint16(sb[ext4MagicOffset:]) != ext4Magic {
		return false, false
	}

	incompat := binary.LittleEndian.Uint32(sb[ext4FeatureIncompatOffset:])

	return true, incompat&ext4FeatureIncompatRecover != 0
}

// readSuperblock reads the primary ext superblock from the block device (or
// image file) at devicePath.
func readSuperblock(devicePath string) ([]byte, error) {
	f, err := os.Open(devicePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sb := make([]byte, ext4SuperblockSize)
	if _, err := f.ReadAt(sb, ext4SuperblockOffset); err != nil {
		return nil, err
	}

	return sb, nil
}

// runE2fsck runs a non-interactive e2fsck (no -f: it replays the journal and
// only escalates to a full check if the replay leaves the filesystem
// inconsistent, so a healthy-but-unreplayed journal exits fast). e2fsck's exit
// code is a
// bitmask: 0 (clean), 0x01 (errors corrected) and 0x02 (corrected, reboot
// recommended) — and their combination 0x03 — all mean the filesystem was made
// consistent, so they are success. Any higher bit (0x04 errors left
// uncorrected, 0x08 operational error, ...) is a failure, since booting such a
// filesystem would still fail.
func runE2fsck(ctx context.Context, devicePath string) error {
	out, err := exec.CommandContext(ctx, "e2fsck", "-y", devicePath).CombinedOutput()
	if err == nil {
		return nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if e2fsckExitOK(exitErr.ExitCode()) {
			return nil
		}

		return fmt.Errorf("exit %d: %s", exitErr.ExitCode(), strings.TrimSpace(string(out)))
	}

	return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
}

// e2fsckExitOK reports whether an e2fsck exit code means the filesystem was made
// consistent. Only the "corrected" (0x01) and "reboot recommended" (0x02) bits
// (and their sum, 0x03) are acceptable; any higher bit is a failure.
func e2fsckExitOK(code int) bool {
	return code&^(e2fsckErrorsCorrected|e2fsckRebootRecommended) == 0
}
