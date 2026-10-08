package referenceexternal_test

import (
	"context"

	referenceexternal "github.com/faustbrian/go-service/integration/reference-external"
	webhook "github.com/faustbrian/go-webhook/v3"
)

// The maintained reference must expose the published major's nominal types,
// rather than merely compile a separate copy of its dependencies.
var (
	_ *webhook.Signer                                                       = referenceexternal.Config{}.WebhookSigner
	_ func(context.Context, []byte, string) (webhook.DeliveryResult, error) = (*referenceexternal.Reference)(nil).DeliverWebhook
)
