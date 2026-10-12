// Package service coordinates service construction, startup, supervision,
// draining, and shutdown.
//
// Start with New for ordered components, or CommandFor and Execute for typed
// process roles. Components and supervised tasks cooperate with cancellation;
// a timeout does not forcibly terminate caller code. Admission closes before
// service-initiated cancellation, tasks join before reverse component cleanup,
// and callers may wait again after a shutdown timeout.
//
// Application routing, authentication, durable delivery, backend clients and
// deployment termination remain application-owned. Optional compositions live
// in separate modules; serverhttp and healthhttp are focused root subpackages.
// Importing service has no side effects or OS signal subscription.
package service
