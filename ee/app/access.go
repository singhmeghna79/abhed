package app

import (
	ee "github.com/zybuu-ai/abhed/ee/server"
	eestore "github.com/zybuu-ai/abhed/ee/store"
	"github.com/zybuu-ai/abhed/app"
	"github.com/zybuu-ai/abhed/config"
	"github.com/zybuu-ai/abhed/server"
	"github.com/zybuu-ai/abhed/store"
)

// Access mounts the invite and access-record surface: administrators mint
// invites, the signup handler redeems them, and the dashboard at /admin says
// who asked, who has access and who used to.
//
// Access records need durable storage, so they exist only on Postgres. A
// type assertion rather than a config check: the question is whether this
// store can keep an access decision across a restart, and the store itself
// is the authority on that. The memory driver cannot, so the dashboard
// explains why instead of appearing empty. Invites work either way; without
// a record behind them they simply link to nothing.
func Access() app.Option {
	return app.WithServerOptions(func(_ config.Config, o *server.Options) error {
		var access ee.AccessStore
		if pg, ok := o.Store.(*store.Postgres); ok {
			access = eestore.NewAccess(pg)
		}
		inv := ee.NewInvites(access)
		o.Invites = inv
		o.Mounts = append(o.Mounts, ee.AccessMount(inv, access))
		o.AdminURL = "/admin"
		return nil
	})
}
