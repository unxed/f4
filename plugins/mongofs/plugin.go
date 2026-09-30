// Package mongofs shows a MongoDB server in a file panel: databases and
// collections are folders, and the documents of a collection are .json files
// (relaxed extended JSON) that F3 views and F5 copies out.
//
// It speaks the wire protocol itself (OP_MSG, SCRAM-SHA-256), so there is no
// driver dependency. The server is named by the MONGODB_URI environment
// variable (default mongodb://127.0.0.1:27017). Documents can be edited (F4),
// created and deleted, and collections created; nothing else is changed.
package mongofs

import (
	"context"
	"errors"

	"github.com/unxed/f4/vfs"
)

// driveName is what the drive menu (Alt+F1) calls the panel.
const driveName = "MongoDB"

const defaultURI = "mongodb://127.0.0.1:27017"

// Plugin registers the MongoDB drive.
type Plugin struct{}

// NewPlugin constructs the built-in MongoDB plugin.
func NewPlugin() *Plugin { return &Plugin{} }

func (*Plugin) GetName() string { return "MongoDB" }

// Init only registers the drive: nothing connects until the panel is opened.
func (*Plugin) Init(api vfs.HostAPI) error {
	if api == nil {
		return errors.New("MongoDB: nil host API")
	}
	api.RegisterDrive(driveName, func() vfs.VFS { return newMongoVFS((&connector{}).open) })
	// mongo:///<path> reopens the panel from a bookmark, history or a saved session.
	return api.RegisterURIProvider(uriProvider{open: func() func(context.Context) (*conn, error) { return (&connector{}).open }})
}

func (*Plugin) Close() error {
	vfs.UnregisterURIProvider("mongo")
	return nil
}

// connectFromEnv opens the connection MONGODB_URI names.
func connectFromEnv(ctx context.Context) (*conn, error) { return (&connector{}).open(ctx) }
