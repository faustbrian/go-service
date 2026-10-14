# Service documentation map

[Repository README](../README.md) · [Current Golib portal](https://github.com/faustbrian/go-library-tools/blob/main/docs/ecosystem/README.md) · [Published ecosystem snapshot](https://github.com/faustbrian/go-library-tools/blob/v1.8.4/docs/ecosystem/README.md)

`service` owns process-local lifecycle, supervision, HTTP serving, and probes.
Applications own configuration, policy, infrastructure clients, transport
handlers, and resource wiring. Import only the packages you need from the root
module; the nested modules below are engineering fixtures, not deployable
applications or separately released libraries.

## Choose and adopt

- [Install and five-minute service](../README.md#install).
- [Adoption guides](adoption.md): HTTP, RPC, worker, ingester, scheduler,
  migration, and mixed-command examples.
- [API reference](api.md), [architecture](architecture.md), and
  [lifecycle and ownership](lifecycle.md).
- [Integration boundaries](integration.md), [HTTP serving](http.md),
  [middleware order](middleware.md), and [resilience composition](resilience.md).
- [Compatibility](compatibility.md), [migration](migration.md),
  [deprecation policy](../DEPRECATION.md), and [changelog](../CHANGELOG.md).
- [FAQ](faq.md) and [support](../SUPPORT.md).

## Operate and investigate

- [Operations](operations.md) and [Kubernetes](kubernetes.md): probes,
  termination, resource ownership, and deployment boundaries.
- [Configuration](configuration.md), [health](health.md),
  [observability](observability.md), and [maintenance mode](maintenance.md).
- [Security guide](security.md), [threat model](threat-model.md), and
  [private vulnerability reporting](../SECURITY.md).
- [Troubleshooting](troubleshooting.md), [performance](performance.md), and
  [HTTP transport comparison](transport-comparison.md).

## Executable composition and evidence

- [Minimal HTTP and ingester/processor recipes](../integration/reference-recipes/README.md).
- [HTTP reference](../integration/reference-http/README.md),
  [external dependency reference](../integration/reference-external/README.md),
  [durability reference](../integration/reference-durability/README.md), and
  [platform reference](../integration/reference-platform/README.md).
- [Track, Postal, and Location adoption fixtures](../integration/adoption/README.md),
  [compatibility harness](../compatibility/README.md), and
  [comparison benchmark methodology](../benchmarks/platform/README.md).
- [Release evidence matrix](evidence.md), [hardening evidence](hardening.md),
  and [source-documentation audit](source-documentation-audit.md).

These references describe their own execution boundaries. Loopback listeners,
in-memory queues, role fixtures, and retained release evidence do not establish
production networking, broker durability, deployed application behavior, or
live backend interoperability. The published
[compatibility set](https://github.com/faustbrian/go-library-tools/blob/v1.8.4/docs/ecosystem/compatibility-sets.md)
is a frozen multi-package baseline, not a claim about every current main head.

## Contribute and release

- [Contributor setup and bounded checks](../CONTRIBUTING.md),
  [testing](testing.md), and [engineering policy](../AGENTS.md).
- [Release boundary](release.md) and [repository compatibility policy](../COMPATIBILITY.md).
- Platform detail: [observable contracts](platform/contracts.md),
  [decisions](platform/decisions.md), [dependency direction](platform/dependencies.md),
  [compatibility and migration](platform/compatibility.md),
  [consumer matrix](platform/consumer-matrix.md),
  [adoption evidence](platform/adoption-evidence.md),
  [adoption budgets](platform/adoption-budgets.md), and
  [performance budgets](platform/performance-budgets.md).

For a new service, continue to the [adoption guides](adoption.md). For an
existing deployment, start with [operations](operations.md) and follow the
linked owner-specific contracts rather than treating a fixture as a runbook.
