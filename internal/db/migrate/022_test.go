package migrate

import "testing"

func TestMigrateGroupPromptFingerprintRulesAddsColumn(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := db.Exec(`CREATE TABLE groups (id integer PRIMARY KEY, name text)`).Error; err != nil {
		t.Fatalf("create legacy groups: %v", err)
	}
	if err := migrateGroupPromptFingerprintRules(db); err != nil {
		t.Fatalf("migrateGroupPromptFingerprintRules: %v", err)
	}
	if err := migrateGroupPromptFingerprintRules(db); err != nil {
		t.Fatalf("migrateGroupPromptFingerprintRules second run: %v", err)
	}
	if !db.Migrator().HasColumn("groups", "system_prompt_fingerprint_rules") {
		t.Fatal("system_prompt_fingerprint_rules column was not added")
	}
	if !db.Migrator().HasColumn("groups", "conversation_rewrite_rules") {
		t.Fatal("conversation_rewrite_rules column was not added")
	}
}
