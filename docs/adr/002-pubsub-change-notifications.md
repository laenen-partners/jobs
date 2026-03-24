# ADR-002: PubSub Change Notifications

**Status:** Accepted
**Date:** 2026-03-20

## Context

The jobs Client needs to notify external consumers (UI, downstream services) when job state changes. The original implementation used a `ChangeNotifier func(jobID string)` callback — a plain function the consuming app wired to whatever invalidation mechanism it had (e.g. `broker.Invalidate("jobs:" + jobID)`).

This worked but had drawbacks:

1. **No structure** — the callback received only a job ID with no indication of what changed (created vs. updated) or who it belonged to (tenant/workspace).
2. **No scoping** — in a multi-tenant system, all notifications went through a single unscoped channel. Consumers had to figure out routing themselves.
3. **Ad-hoc wiring** — every consuming app reimplemented the same pubsub publish logic inside the callback.

Meanwhile, the organisation's [`pubsub`](https://github.com/laenen-partners/pubsub) library (v0.3.1) provides a typed messaging layer with:

- `ChangeNotification` (entity, entityID, action) as a standard wire format
- Scoped topics: `<tenant>.<workspace>.change.<entity>.<entityID>.<action>`
- Convenience methods: `NotifyCreated`, `NotifyUpdated`, etc.
- `ScopeProvider` interface satisfied by `identity.Context` out of the box

## Decision

Replace the `ChangeNotifier` callback with direct pubsub integration:

1. **Accept a `pubsub.PubSub` transport** via `WithPubSub(ps)` client option
2. **Derive scope from `identity.Context`** on the request context — no separate scope configuration needed
3. **Create a scoped `Bus` per notification** — lightweight (struct allocation only, no connections or goroutines)
4. **Publish structured `ChangeNotification`s** with entity `"jobs"`, the job ID, and the appropriate action

### What changed in `client.go`

```go
// Before
type ChangeNotifier func(jobID string)

type Client struct {
    // ...
    onChange ChangeNotifier
}

// After
type Client struct {
    // ...
    ps pubsub.PubSub
}
```

The `notifyChange` method:

```go
func (c *Client) notifyChange(ctx context.Context, jobID, action string) {
    if c.ps == nil {
        return
    }
    id, ok := identity.FromContext(ctx)
    if !ok {
        // warn and skip — no scope available
        return
    }
    bus := pubsub.NewBus(c.ps, "jobs", pubsub.WithScopeFrom(id))
    // publishes to: <tenant>.<workspace>.change.job.<jobID>.<action>
    switch action {
    case pubsub.ActionCreated:
        bus.NotifyCreated(ctx, "jobs", jobID)
    default:
        bus.NotifyUpdated(ctx, "jobs", jobID)
    }
}
```

### Action mapping

| Mutation | Action |
|---|---|
| `RegisterJob` | `created` |
| `FinalizeJob` | `updated` |
| `ReportProgress` | `updated` |
| `RegisterStep` | `updated` |
| `AddTags` | `updated` |
| `TrackStep` (complete/fail) | `updated` |

All mutations other than initial job creation are `updated` — consumers re-query for current state rather than relying on the notification payload.

### Wiring

```go
client := jobs.NewClient(store, jobs.WithPubSub(ps))
```

The transport (`ps`) is typically shared with other parts of the application (e.g. the SSE stream broker). The Client stores only the transport; the scoped `Bus` is created per notification using the identity from the request context.

### No identity, no notification

If `identity.FromContext(ctx)` returns false (e.g. background jobs, migrations, seed scripts), the notification is skipped with a warning log. This is intentional — without identity there is no tenant/workspace scope, and publishing to an unscoped topic would be incorrect.

### Best-effort tracking caveat

`TrackRun` and `TrackStep` treat tracking as secondary to the actual work — store failures are retried and then swallowed. This means:

- A notification may fire for a mutation that was retried (duplicate notifications are possible)
- If all retries fail, the notification still fires for the attempt, but the store state may be stale
- The UI shows an info tooltip on "Pipeline Steps" explaining that step status is best-effort

The direct methods (`RegisterJob`, `FinalizeJob`, etc.) return errors and only notify on success — they provide accurate notifications.

## Consequences

### Positive

- **Structured notifications** — consumers know what entity changed, its ID, and the action, without re-querying just to determine what happened
- **Multi-tenant scoping** — topics are automatically scoped to tenant/workspace, so subscribers only receive notifications for their scope
- **No custom wiring** — consuming apps pass a transport; the SDK handles publish logic, topic construction, and scope resolution
- **Standard wire format** — uses the same `ChangeNotification` envelope as all other services using the pubsub library
- **Zero overhead when unused** — `WithPubSub` is optional; without it, `notifyChange` is a nil check and return

### Negative

- **New dependencies** — the core package now imports `pubsub` and `identity`, breaking the previous "zero heavy deps" property. Both are lightweight org-internal packages with no transitive dependencies of concern.
- **Per-notification Bus allocation** — a new `Bus` struct is created for each notification to carry the correct scope. This is a single struct allocation with no I/O, but it is allocation nonetheless.
- **Silent skip without identity** — background processes that mutate jobs without identity context will not emit notifications. This is correct behaviour but may surprise operators if they expect notifications from CLI tools or migration scripts.
