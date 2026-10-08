package referencedurability_test

import (
	"testing"

	idempotencyoutbox "github.com/faustbrian/go-idempotency/v2/adapters/outbox"
	outboxqueue "github.com/faustbrian/go-transactional-outbox/adapters/queue/v2"
	outbox "github.com/faustbrian/go-transactional-outbox/v2"
	outboxpostgres "github.com/faustbrian/go-transactional-outbox/v2/postgres"
	"github.com/faustbrian/go-transactional-outbox/v2/relay"
)

// These nominal contracts join the actual published modules used by both
// normal staging and fresh-process recovery; similar old-major types cannot.
func TestOutboxV2DurabilityComposition(t *testing.T) {
	var _ idempotencyoutbox.Writer[outbox.Envelope] = (*outboxpostgres.Writer)(nil)
	var _ relay.Publisher = (*outboxqueue.Publisher)(nil)
}
