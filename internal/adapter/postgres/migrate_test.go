package postgres

import "testing"

func TestMigrateURL(t *testing.T) {
	tests := map[string]string{
		"postgres://yesbook@localhost/yesbook":   "pgx5://yesbook@localhost/yesbook",
		"postgresql://yesbook@localhost/yesbook": "pgx5://yesbook@localhost/yesbook",
		"pgx5://yesbook@localhost/yesbook":       "pgx5://yesbook@localhost/yesbook",
		"postgres://yesbook@localhost/yesbook?x": "pgx5://yesbook@localhost/yesbook?x",
	}
	for in, want := range tests {
		if got := migrateURL(in); got != want {
			t.Errorf("migrateURL(%q) = %q, want %q", in, got, want)
		}
	}
}
