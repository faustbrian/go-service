// Package platform provides synthetic in-process comparison fixtures selected
// by Candidates. Each endpoint owns only its local adapter state; callers own
// request/response-body cleanup and optional logger/trace behavior. Fiber uses
// fasthttp and is disclosed separately, not as full net/http interoperability.
// Released dependency bindings are retained by this non-releasable module.
package platform
