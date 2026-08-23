// Package azure holds cross-cutting Azure helpers shared by every Azure consumer.
package azure

import (
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// DefaultCredential returns a process-shared DefaultAzureCredential: azidentity
// caches AAD tokens per instance, so per-consumer instances would each re-probe
// the credential chain for tokens a sibling already holds.
var DefaultCredential = sync.OnceValues(func() (azcore.TokenCredential, error) {
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure default credential: %w", err)
	}

	return credential, nil
})
