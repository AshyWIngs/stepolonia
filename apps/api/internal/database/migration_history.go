package database

import (
	"context"
	"database/sql"
	"fmt"
)

const finalMigrationVersion int64 = 6

type migrationEvent struct {
	version int64
	applied bool
}

func migrationVersion(events []migrationEvent) (int64, error) {
	var current int64
	for index, event := range events {
		next, err := nextMigrationVersion(current, index, event)
		if err != nil {
			return 0, err
		}
		current = next
	}
	if current != 0 && current != finalMigrationVersion {
		return 0, fmt.Errorf("partial Goose migration set at version %d; recreate the disposable database", current)
	}
	return current, nil
}

func readMigrationVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var history bool
	if err := db.QueryRowContext(ctx, "SELECT pg_catalog.to_regclass('public.goose_db_version') IS NOT NULL").Scan(&history); err != nil {
		return 0, err
	}
	if !history {
		return 0, nil
	}
	rows, err := db.QueryContext(ctx, "SELECT version_id,is_applied FROM public.goose_db_version ORDER BY id")
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var events []migrationEvent
	for rows.Next() {
		var event migrationEvent
		if err := rows.Scan(&event.version, &event.applied); err != nil {
			return 0, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return migrationVersion(events)
}

func nextMigrationVersion(current int64, index int, event migrationEvent) (int64, error) {
	if event.version < 0 || event.version > finalMigrationVersion {
		return 0, fmt.Errorf("unsupported Goose migration version %d", event.version)
	}
	if event.version == 0 {
		if index != 0 || !event.applied {
			return 0, fmt.Errorf("invalid Goose baseline history")
		}
		return current, nil
	}
	if event.applied {
		if event.version != current+1 {
			return 0, fmt.Errorf("noncontiguous or out-of-order Goose history")
		}
		current = event.version
	} else {
		if event.version != current {
			return 0, fmt.Errorf("out-of-order Goose rollback history")
		}
		current--
	}
	return current, nil
}
