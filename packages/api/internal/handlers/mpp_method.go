package handlers

import (
	"context"
	"fmt"

	"github.com/tempoxyz/mpp-go/mpp"
	mppserver "github.com/tempoxyz/mpp-go/server"
)

// TempoMethod implements mppserver.Method for the Tempo payment method.
type TempoMethod struct {
	currency  string
	recipient string
}

// NewTempoMethod creates a TempoMethod with the given currency and recipient.
func NewTempoMethod(currency, recipient string) mppserver.Method {
	return &TempoMethod{
		currency:  currency,
		recipient: recipient,
	}
}

func (m *TempoMethod) Name() string {
	return "tempo"
}

func (m *TempoMethod) Intents() map[string]mppserver.Intent {
	return map[string]mppserver.Intent{
		"charge": &TempoChargeIntent{
			currency:  m.currency,
			recipient: m.recipient,
		},
	}
}

// TempoChargeIntent implements mppserver.Intent for the charge intent.
type TempoChargeIntent struct {
	currency  string
	recipient string
}

func (i *TempoChargeIntent) Name() string {
	return "charge"
}

func (i *TempoChargeIntent) Verify(ctx context.Context, credential *mpp.Credential, request map[string]any) (*mpp.Receipt, error) {
	// Verify that the credential contains a valid transaction signature.
	// For now, accept any credential that passes HMAC challenge verification
	// (handled by the mpp-go server library). Full on-chain verification
	// can be added by checking the transaction on the Tempo RPC.
	if credential == nil {
		return nil, fmt.Errorf("missing credential")
	}

	txHash := ""
	if credential.Payload != nil {
		if sig, ok := credential.Payload["signature"].(string); ok {
			txHash = sig
		}
	}

	return mpp.Success(txHash, mpp.WithReceiptMethod("tempo")), nil
}
