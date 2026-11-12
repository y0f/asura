package httputil

import (
	"context"

	"github.com/y0f/asura/v3/internal/storage"
)

// MonitorAccess translates one validated action at a time into a storage-owned
// filter. Missing identities, unknown actions and an empty action list always
// produce an explicit deny filter, never the trusted internal unfiltered mode.
func MonitorAccess(ctx context.Context, actions ...string) []storage.MonitorAccess {
	if len(actions) == 0 {
		return []storage.MonitorAccess{{}}
	}
	key := GetAPIKey(ctx)
	filters := make([]storage.MonitorAccess, len(actions))
	for i, action := range actions {
		access := key.AccessFor(action)
		filters[i] = storage.MonitorAccess{All: access.All, MonitorIDs: access.MonitorIDs, GroupIDs: access.GroupIDs}
	}
	return filters
}
