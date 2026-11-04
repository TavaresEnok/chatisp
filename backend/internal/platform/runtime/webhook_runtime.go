package runtime

import (
	"context"

	"chatisp/internal/outwebhook"
)

// startOutboundWebhooks puts unmarked sources back on the webhooks queue.
// Delivery itself runs on the process River client. A second client would
// share leader election and discard kinds it does not register. A missing
// database, key ring or store skips recovery so API-only processes keep running.
func startOutboundWebhooks(ctx context.Context, opts Options) error {
	if opts.Config.JobsDatabaseURL == "" || opts.Config.KeyRing == nil || opts.Store == nil {
		return nil
	}
	if _, err := outwebhook.RecoverSources(ctx, opts.Store); err != nil {
		return err
	}
	return nil
}
