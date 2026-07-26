# Migration from ad hoc `net/http`

1. Keep the existing router as an `http.Handler`; no route migration is needed.
2. Move listener construction to the composition root and pass it to
   `serverhttp.New`.
3. Replace an unbounded `ListenAndServe` goroutine with `Server.Run` registered
   through `Service.Go`.
4. Convert dependency start/close pairs into named `service.Component` values
   in startup order.
5. Replace scattered signal channels with `service.Wait` after supervised work
   is registered, or `service.Run` for component-only processes.
6. Mount `healthhttp` handlers and use the service itself as `Lifecycle`.
7. Remove duplicate panic, body-limit, and request-ID middleware only after
   verifying ordering and trusted-proxy policy.

During migration, keep the old and new shutdown paths mutually exclusive. A
listener, goroutine, channel, timer, provider, or dependency must have one
owner. Test partial startup and shutdown before removing the old wiring.

Behavioral differences to plan for:

- startup failure now rolls back successful components in reverse order;
- repeated or concurrent shutdown returns one retained terminal result;
- readiness becomes false as soon as drain begins;
- panic and health responses deliberately omit internal error text;
- inbound request IDs are replaced unless trust is explicitly configured;
- explicit zero HTTP timeouts disable those individual `net/http` bounds, but
  shutdown must always remain positive and bounded.
