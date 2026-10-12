// Package referencedurability composes Golib durability primitives through
// their published public APIs for non-production operational-assurance fixtures.
// Run and recovery entrypoints mutate a disposable PostgreSQL database and Valkey
// Stream with fixed application identities; never target production state.
// Migration/connection parsing permits native environment/TLS inputs. External
// harnesses own dependency provisioning, restart/kill and final state cleanup;
// this package does not establish exactly-once delivery for arbitrary workloads.
package referencedurability
