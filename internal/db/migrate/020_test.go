package migrate

import "testing"

func TestMigrateGroupPromptSanitizeAddsColumn(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := db.Exec(`CREATE TABLE groups (id integer PRIMARY KEY, name text)`).Error; err != nil {
		t.Fatalf("create legacy groups: %v", err)
	}
	if err := migrateGroupPromptSanitize(db); err != nil {
		t.Fatalf("migrateGroupPromptSanitize: %v", err)
	}
	if !db.Migrator().HasColumn("groups", "system_prompt_sanitize_fingerprints") {
		t.Fatal("system_prompt_sanitize_fingerprints column was not added")
	}
}
