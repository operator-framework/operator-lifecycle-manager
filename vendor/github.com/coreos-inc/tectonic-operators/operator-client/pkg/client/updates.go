package client

// UpdateOpts holds all the information needed to perform an
// update on a given object.
type UpdateOpts struct {
	// Migrations to be ran for update.
	Migrations *MigrationList
}
