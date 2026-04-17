// Package drivers is a side-effect import that registers every
// first-class datastore driver bundled with OpsIntelligence. Callers
// that want "whatever the config asks for" should import this package
// once near main and then use datastore.Open:
//
//	import _ "github.com/opsintelligence/opsintelligence/internal/datastore/drivers"
//
// Each driver can still be imported directly if an operator wants to
// trim build-time deps — this file just keeps main.go tidy.
package drivers

import (
	_ "github.com/opsintelligence/opsintelligence/internal/datastore/driver/postgres"
	_ "github.com/opsintelligence/opsintelligence/internal/datastore/driver/sqlite"
)
