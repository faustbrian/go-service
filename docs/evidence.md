# Release evidence matrix

This matrix maps the `v1` promises to their implementation, executable proof,
and public contract. Symbol and test names are used instead of line numbers so
the evidence remains stable across formatting changes. A passing percentage or
workflow is supporting evidence only; the named regression defines the
behavior being proved.

## Authoritative contracts

- Go [`context`](https://pkg.go.dev/context),
  [`net/http`](https://pkg.go.dev/net/http),
  [`os/signal`](https://pkg.go.dev/os/signal), and
  [`log/slog`](https://pkg.go.dev/log/slog) documentation;
- Kubernetes [probe
  semantics](https://kubernetes.io/docs/concepts/configuration/liveness-readiness-startup-probes/)
  and [pod termination
  flow](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/#pod-termination-flow);
- `.ai/GOAL.md`, `.ai/GOAL_HARDEN.md`, exported APIs, examples, workflows,
  and the public guides in this repository.

## Lifecycle and concurrency

| Promise | Implementation | Executable evidence | Public contract |
| --- | --- | --- | --- |
| safe zero and explicit invalid configuration | `service.New`, zero `Service` paths | `TestNewRejectsInvalidConfiguration`, `TestZeroServiceShutdownIsSafe`, `TestZeroServiceUsesSafeSupervisionDefault`, `FuzzConfig` | `docs/api.md`, `docs/lifecycle.md` |
| unique ordered components | `Config.Components`, `Service.Start` | `TestServiceStartsAndStopsComponentsInOwnershipOrder`, duplicate cases in `TestNewRejectsInvalidConfiguration` | `docs/lifecycle.md` |
| partial-start rollback in reverse order | `failStartup`, `beginRollback`, `stopComponents` | `TestStartRollsBackOwnedComponentsAndPreservesFailures` | `docs/lifecycle.md` |
| bounded and joinable rollback | `Config.RollbackTimeout`, rollback coordinator | `TestStartupRollbackTimeoutRemainsJoinable` | `docs/lifecycle.md`, `docs/operations.md` |
| cancellation stops later startup | post-hook cause check in `Start` | `TestShutdownDuringStartupDoesNotStartLaterComponents` | `docs/lifecycle.md` |
| start and stop panic containment | `invoke`, `PanicError` | `TestComponentPanicsAreContainedAndCleanupContinues` | `docs/lifecycle.md`, `docs/security.md` |
| explicit state transitions | `State`, `Ready`, `Drain`, `Shutdown` | `TestLifecycleTransitionsAreExplicitAndRepeatable` | `docs/lifecycle.md` |
| concurrent state operations | service mutex and terminal channels | `TestConcurrentLifecycleOperationsRespectStartingAndDraining`, `TestConcurrentShutdownRunsCleanupOnceAndBoundsEachWaiter` | `docs/lifecycle.md` |
| abandoned shutdown caller can rejoin | owned stop coordinator | `TestShutdownCallerCanAbandonUncooperativeComponent`, `TestStartupShutdownWaiterCanAbandonWait` | `docs/lifecycle.md` |
| typed aggregate failures | `StartupError`, `ShutdownError`, multi-`Unwrap` | `TestLifecycleErrorContractsAndInvalidOperations`, rollback and supervised-failure tests | `docs/api.md`, `docs/troubleshooting.md` |
| bounded supervised work | `Config.MaxTasks`, `Service.Go` | `TestSupervisedTasksRespectConfiguredBound`, `FuzzConfig` | `docs/lifecycle.md` |
| task failure and panic drain the service | `Service.Go` coordinator | `TestSupervisedFailureDrainsAndPreservesCause`, `TestSupervisedPanicIsRedactedAndReturned` | `docs/lifecycle.md`, `docs/security.md` |
| successful finite task completion is not failure | nil task result path | `TestSupervisedTaskMayReturnSuccessfullyBeforeShutdown` | `docs/lifecycle.md` |
| task cancellation results are normal shutdown | cancellation-aware task result classification | `TestSupervisedTaskCancellationResultIsNormalShutdown` | `docs/lifecycle.md`, `docs/integration.md` |
| shutdown joins supervised work | task count and `shutdownDone` | `TestSupervisedWorkCancelsServiceAndIsJoined` | `docs/lifecycle.md` |
| parent, signal, and service causes survive | cancel-cause contexts and `SignalError` | `TestRunStopsWhenParentIsCanceled`, `TestRunWithSignalsPreservesSignalCauseAndShutdownBound`, `TestWaitWithSignalsStopsAfterSupervisedTaskFailure` | `docs/lifecycle.md` |
| owned signal subscription and storm handling | `Run`, `Wait`, deferred `signal.Stop` | `TestRunReceivesProcessSignal`, `TestRunWithSignalsHandlesSignalStormOnce`, nil-signal validation tests | `docs/lifecycle.md` |

## HTTP and middleware

| Promise | Implementation | Executable evidence | Public contract |
| --- | --- | --- | --- |
| secure independent defaults | `defaultConfig`, `http.Server` construction | `TestNewAppliesSecureDefaultsWithoutStartingWork`, `TestNewAppliesEveryExplicitServerOption`, `FuzzOptions` | `docs/http.md`, `docs/security.md` |
| slow header, body, response, and idle bounds | standard server timeout fields | `TestReadHeaderTimeoutClosesSlowHeaders`, `TestReadTimeoutBoundsSlowRequestBody`, `TestWriteTimeoutBoundsUnreadResponse`, `TestIdleTimeoutClosesKeepAliveConnection` | `docs/http.md` |
| bounded hostile headers | `MaxHeaderBytes` | `TestMaxHeaderBytesRejectsHostileHeader` | `docs/http.md`, `docs/security.md` |
| graceful drain then forced close | `Server.Run`, `Shutdown`, `Server.Close` | `TestRunServesRealListenerAndDrainsActiveRequest`, `TestRunForceClosesAfterShutdownTimeout`, `TestRunAggregatesForcedCloseFailure` | `docs/http.md`, `docs/operations.md` |
| no abandoned pre-run listener | `Server.Close` | `TestCloseBeforeRunReleasesOwnedListener`, `TestRunTreatsExplicitServerCloseAsNormal` | `docs/http.md` |
| request and disconnect cancellation | default `BaseContext`, standard request context | `TestRunContextPropagatesToRequestHandlers`, `TestClientDisconnectCancelsRequest` | `docs/http.md` |
| HTTP/1, standard HTTP/2, and hijacking | caller-owned `http.Server.Protocols`, recovery `Unwrap` | `TestRunSupportsStandardLibraryUnencryptedHTTP2`, `TestRecoveryPreservesHijacking` | `docs/http.md`, `docs/compatibility.md` |
| visible deterministic middleware order | `Chain`, constructor stack | `TestMiddlewareOrderRequestIDsAndBodyLimits`, `TestDuplicateMiddlewareInstallationRemainsVisible` | `docs/http.md`, `docs/middleware.md` |
| nil middleware is explicit | `Chain`, constructor validation | `TestMiddlewareValidationAndFailurePaths`, `TestNewRejectsMiddlewareReturningNil` | `docs/http.md` |
| bounded trusted request IDs | `RequestIDs`, token validation | `TestRequestIDTrustRejectsHeaderInjection`, `FuzzRequestIDs`, `TestDefaultRequestIDIsValid` | `docs/http.md`, `docs/security.md` |
| body limit before application reads | `LimitBody`, `http.MaxBytesReader` | `TestMiddlewareOrderRequestIDsAndBodyLimits`, `TestBodyLimitCoversStreamingAndDisabledBodies` | `docs/http.md` |
| secret-safe panic response | `Recover`, tracked writer | `TestRecoveryDoesNotLeakPanicOrPreparedHeaders`, `TestRecoveryPreservesCommittedResponseAndUnwrapsWriter` | `docs/http.md`, `docs/security.md` |

## Health and integration

| Promise | Implementation | Executable evidence | Public contract |
| --- | --- | --- | --- |
| stable liveness, startup, readiness JSON | `Probes` handlers and `Response` | `TestProbeHandlersFollowLifecycleAndHideCheckErrors`, `FuzzHealthPayload` | `docs/health.md`, `docs/kubernetes.md` |
| failed startup never becomes probe success | startup state mapping | `TestStartupProbeRejectsFailedLifecycle` | `docs/health.md` |
| concurrent and sequential bounds | pre-scheduling shared semaphore, `MaxChecks`, modes | `TestConcurrentChecksRespectBoundsAndRegistrationOrder`, `TestSequentialChecksDoNotOverlap`, `TestConcurrentChecksQueueWithinConcurrencyBound`, `TestConcurrentChecksBoundScheduledGoroutines`, `TestSequentialChecksRespectGlobalConcurrencyAfterCancellation` | `docs/health.md` |
| stuck and panicking checks are contained | per-check context, semaphore quarantine, `runCheck` recovery | `TestIgnoringAndPanickingChecksAreBoundedAndRedacted` | `docs/health.md`, `docs/security.md` |
| dependency recovery restores readiness | fresh check evaluation per request | `TestRecoveringCheckBecomesReadyAgain` | `docs/health.md` |
| configuration failure prevents partial startup without leaking sensitive source text | `integration.New` as first component and `config` sensitive-source errors | `TestHookComponentPreventsPartialStartupAndKeepsCleanupOrdered`, `TestActualConfigurationFailureIsRedactedAndPreventsStartup`, `ExampleNew` | `docs/configuration.md`, `docs/integration.md` |
| hooks preserve context and errors | direct caller-owned hook invocation | `TestHooksReceiveCallerContext`, `TestHookComponentRunsSuccessAndStopFailurePaths` | `docs/integration.md` |
| real optional modules preserve order, cancellation, redaction, duplicate registration policy, and dependency direction | isolated `compatibility` module and pinned workflow | `TestActualHTTPMiddlewareContracts`, `TestActualLifecycleIntegrationContracts`, `TestActualConfigurationFailureIsRedactedAndPreventsStartup`, `TestActualTelemetryDuplicateRegistrationRollsBackStartup`, `make integration-compatibility` | `docs/integration.md`, `docs/compatibility.md` |
| caller-owned slog with bounded attributes | `WithSlog` | `TestSlogOptionReportsStatusWithoutErrorValues`, `FuzzOptions`, `ExampleWithSlog` | `docs/integration.md`, `docs/security.md` |
| duplicate logger ownership is explicit | integration option state | duplicate logger case in `TestIntegrationConfigurationValidation` | `docs/integration.md` |
| no provider, exporter, handler, config, auth, or policy ownership | dependency-neutral `Hooks`; no SDK imports | `go list -deps ./...`, executable cross-cutting examples | `docs/architecture.md`, `docs/configuration.md`, `docs/integration.md`, `docs/middleware.md` |
| independent package graph and no initialization side effects | exact non-standard dependency allowlists and AST inspection | `TestProductionDependencyBoundaries`, `TestProductionPackagesHaveNoInitializers` | `docs/architecture.md` |

## Test utilities, resources, and scenarios

| Promise | Executable evidence or artifact |
| --- | --- |
| deterministic barriers without timing sleeps | `TestBarrierSupportsReleaseAndCancellationWithoutSleeps`, `TestBarrierSupportsConcurrentFirstWaiters` |
| bounded probe response capture during writes | `TestProbeWriterNeverRetainsPastLimit`, `TestProbeCapturesBoundedResponse` |
| listener and serving goroutine closure | pre-run close, graceful drain, forced-close, and active-close tests above |
| probe goroutine scheduling and join bounds | `TestConcurrentChecksBoundScheduledGoroutines` runs in a `testing/synctest` bubble, counts scheduled check work, and cannot complete with an unjoined bubble goroutine |
| timeout timer release on completed work | `TestCompletedCheckCancelsItsTimeoutContext`, rollback context assertion in `TestStartRollsBackOwnedComponentsAndPreservesFailures`, shutdown context assertion in `TestRunWithSignalsPreservesSignalCauseAndShutdownBound` |
| HTTP client response bodies in real-listener tests | explicit successful `response.Body.Close` assertions in the graceful, timeout, header-bound, HTTP/2, and forced-close suites; inbound bodies remain `net/http` owned |
| cancellation-ignoring caller work remains visible and bounded | uncooperative component, supervised task, and health-check tests above; documented residual contracts |
| HTTP API and Kubernetes probes | `examples/http-api`, `docs/kubernetes.md` |
| RPC service | real `net/rpc` listener in `examples/rpc` |
| worker | `examples/worker` |
| ingester | `examples/ingester` |
| scheduled command | `examples/scheduled-command` |
| mixed-role service | HTTP, consumer, processor, and scheduler in `examples/mixed-role` |
| authentication and authorization middleware | executable `ExampleChain_authenticationAndAuthorization` |
| configuration and observability hooks | executable `ExampleNew` and `ExampleWithSlog` |
| queue and scheduler lifecycle adapters | executable `ExampleNew_queueAndScheduler` |

## Repository and release gates

| Gate | Authoritative command or artifact |
| --- | --- |
| formatting, vet, lint, tests, exact coverage, race | `make check`, `scripts/check-coverage.sh` |
| no production unsafe, cgo, or linkname | `make safety`, `scripts/check-go-safety.sh` |
| fuzz-target smoke | `make fuzz`, scheduled `.github/workflows/fuzz.yml` |
| allocation benchmarks and budgets | `make benchmark`, allocation budget tests, `docs/performance.md` |
| required docs, API comments, executable examples | `make docs`, `scripts/check-docs.sh`, `scripts/check-api-docs.go` |
| workflow contracts | `make workflows`, pinned `actionlint` v1.7.12 |
| reachable vulnerabilities and dependency review | `make vuln`, `.github/workflows/security.yml` |
| isolated optional integration drift and vulnerabilities | `make integration-compatibility`, `.github/workflows/integrations.yml` |
| minimum/current Go and OS matrix | `.github/workflows/ci.yml` |
| signed tag, changelog, provenance, deterministic archive | `scripts/release.sh`, `.github/workflows/release.yml`, `docs/release.md` |

Hosted results and release publication are not inferred from local commands.
The final verdict in `docs/hardening.md` records their current state explicitly.
