// Package referenceexternal provides the maintained non-production reference
// composition for outbound dependencies and application-owned secret storage.
// Start with New using trusted endpoints and borrowed storage/secret/webhook
// policy. Close releases selected owned policies/client but is not a general
// drain barrier for all storage, hedge or webhook calls. Published dependency
// pins and synthetic workload limits belong to this fixture, not root Service.
package referenceexternal
