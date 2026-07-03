package migrate

import "gorm.io/gorm"

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 6,
		Up:      migrateSiteAccountsAddSub2APIRefreshFields,
	})
}

// 006 used to add Sub2API refresh fields to site_accounts. Credentials now live
// in site_credentials, so this migration intentionally remains a no-op while
// preserving the version record for existing databases.
func migrateSiteAccountsAddSub2APIRefreshFields(db *gorm.DB) error {
	_ = db
	return nil
}
