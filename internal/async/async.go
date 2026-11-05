// Package async holds the reply a service request gives when its work must
// run off the main thread.
package async

// Reply is a reply worked out off the main thread, for requests that wait on
// other programs; the service runs it before it replies.
type Reply func() (any, error)
