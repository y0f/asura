package checker

import (
	"context"
	"io"
)

// closeConnOnCancel binds an established socket to the check's context.
// DialContext only covers connection establishment; deadlines alone do not
// interrupt a read when shutdown cancels a context before its deadline.
// Call the returned cleanup on every exit to detach the callback and close the
// socket. Network connections permit concurrent Close calls.
func closeConnOnCancel(ctx context.Context, conn io.Closer) func() {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return func() {
		stop()
		_ = conn.Close()
	}
}
