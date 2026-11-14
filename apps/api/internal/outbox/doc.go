// Package outbox is the transactional outbox: owning features record messages in the same
// transaction as their business change, and the scheduler tick relays them to the sender
// registered for each topic, deferring around quiet hours, retrying with backoff and expiring
// stale work. It never imports the scheduler (constitution IV).
//
// # Adding a message from an owning feature
//
// Register a Sender per topic at wiring time, then call Add inside the business transaction:
//
//	ob.RegisterSender("task.reminder", sender, 5)
//	err := txr.InTx(ctx, func(ctx context.Context) error {
//		// ... business change ...
//		_, err := ob.Add(ctx, outbox.Message{ /* org, topic, dedupe key, recipient, envelope */ })
//		return err
//	})
//
// A message whose (org, topic, dedupe key) was already recorded is ignored (Add returns false).
//
// # Sender idempotency contract
//
// Delivery is at-least-once: a Sender may be called again for the same message after a crash or
// retry, so it must tolerate duplicates (use the message dedupe key as the provider idempotency
// key where one exists).
package outbox
