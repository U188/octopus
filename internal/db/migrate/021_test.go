package migrate

import "testing"

func TestMigrateRelayLogPromptRetryFieldsAddsColumns(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := db.Exec(`CREATE TABLE relay_logs (id integer PRIMARY KEY)`).Error; err != nil {
		t.Fatalf("create legacy relay_logs: %v", err)
	}
	if err := migrateRelayLogPromptRetryFields(db); err != nil {
		t.Fatalf("migrateRelayLogPromptRetryFields: %v", err)
	}
	for _, column := range []string{"system_prompt_retry", "system_prompt_retry_reason"} {
		if !db.Migrator().HasColumn("relay_logs", column) {
			t.Fatalf("%s column was not added", column)
		}
	}
}
