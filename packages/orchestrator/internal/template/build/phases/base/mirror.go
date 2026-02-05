package base

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	gcpMetadataZoneURL = "http://metadata.google.internal/computeMetadata/v1/instance/zone"
	metadataTimeout    = 5 * time.Second
)

// getGCPRegion queries the GCP metadata server to get the current region.
// Returns empty string if not running on GCP or if the query fails.
var getGCPRegion = sync.OnceValue(func() string {
	ctx, cancel := context.WithTimeout(context.Background(), metadataTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gcpMetadataZoneURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	// Format: projects/<project-number>/zones/<zone>
	zone := string(body)
	parts := strings.Split(zone, "/")
	if len(parts) < 4 {
		return ""
	}

	// Extract region from zone (us-central1-a -> us-central1)
	zoneName := parts[len(parts)-1]
	lastDash := strings.LastIndex(zoneName, "-")
	if lastDash == -1 {
		return ""
	}

	return zoneName[:lastDash]
})

// GetMirrorSetupScript returns a shell script for configuring cloud-provider-optimized
// APT mirrors. Returns empty string if no optimization is available.
func GetMirrorSetupScript() string {
	provider := storage.Provider(env.GetEnv("STORAGE_PROVIDER", string(storage.DefaultStorageProvider)))

	switch provider {
	case storage.GCPStorageProvider:
		region := getGCPRegion()
		if region == "" {
			return ""
		}
		return fmt.Sprintf(gcpMirrorSetupScriptTemplate, region, region)
	case storage.AWSStorageProvider:
		// TODO: Add AWS mirror support
		return ""
	default:
		return ""
	}
}

// gcpMirrorSetupScriptTemplate configures GCE regional APT mirrors.
// Supports Ubuntu and Debian. Falls back gracefully if mirror is not accessible.
// Template args: region (twice - for mirror URL and apt pin origin).
const gcpMirrorSetupScriptTemplate = `
echo "Configuring GCP-optimized APT mirror"

GCP_REGION="%s"

if [ -f /etc/os-release ]; then
    . /etc/os-release
    DISTRO_ID="${ID:-}"
    CODENAME="${VERSION_CODENAME:-}"
else
    echo "Could not detect OS, skipping mirror configuration"
    DISTRO_ID=""
    CODENAME=""
fi

configure_ubuntu_mirror() {
    GCE_MIRROR="http://${GCP_REGION}.gce.archive.ubuntu.com/ubuntu"
    
    if curl -s --connect-timeout 5 --max-time 10 -o /dev/null -w "%%{http_code}" "$GCE_MIRROR/dists/" 2>/dev/null | grep -q "200"; then
        echo "Using GCE Ubuntu mirror: $GCE_MIRROR"
        
        cat > /etc/apt/sources.list.d/gce-mirror.list << MIRROREOF
deb $GCE_MIRROR $CODENAME main restricted universe multiverse
deb $GCE_MIRROR $CODENAME-updates main restricted universe multiverse
deb http://security.ubuntu.com/ubuntu $CODENAME-security main restricted universe multiverse
MIRROREOF
        cat > /etc/apt/preferences.d/gce-mirror << MIRROREOF
Package: *
Pin: origin %s.gce.archive.ubuntu.com
Pin-Priority: 900
MIRROREOF
        return 0
    fi
    return 1
}

configure_debian_mirror() {
    GCE_DEBIAN_MIRROR="http://deb.debian.org/debian"
    
    if curl -s --connect-timeout 5 --max-time 10 -o /dev/null -w "%%{http_code}" "$GCE_DEBIAN_MIRROR/dists/" 2>/dev/null | grep -q "200"; then
        echo "Configuring Debian mirror"
        
        cat > /etc/apt/sources.list.d/gce-mirror.list << MIRROREOF
deb $GCE_DEBIAN_MIRROR $CODENAME main contrib non-free non-free-firmware
deb $GCE_DEBIAN_MIRROR $CODENAME-updates main contrib non-free non-free-firmware
deb http://security.debian.org/debian-security $CODENAME-security main contrib non-free non-free-firmware
MIRROREOF
        return 0
    fi
    return 1
}

if [ -n "$CODENAME" ]; then
    case "$DISTRO_ID" in
        ubuntu)
            if ! configure_ubuntu_mirror; then
                echo "GCE mirror not accessible, using defaults"
            fi
            ;;
        debian)
            if ! configure_debian_mirror; then
                echo "Debian mirror not accessible, using defaults"
            fi
            ;;
        *)
            echo "Unsupported distribution: $DISTRO_ID"
            ;;
    esac
else
    echo "Could not determine distribution codename"
fi
`
