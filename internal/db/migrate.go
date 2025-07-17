package db

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed schema.sql
var ddl string

func Migrate(ctx context.Context, sqlDB *sql.DB) error {
	if _, err := sqlDB.ExecContext(ctx, ddl); err != nil {
		return err
	}

	// https://readnovelfull.com/shadow-slave/chapter-2460-on-the-count-of-three.html
	// // Add user 1 if not exists
	// if _, err := sqlDB.ExecContext(ctx, `
	// 	INSERT OR IGNORE INTO users (id, username, password) VALUES (1, 'default', 'password')
	// `); err != nil {
	// 	return err
	// }

	// now := time.Now().Unix()

	// // Add three items for user 1
	// items := []string{
	// 	"https://egemengol.com/blog/kindlepathy/",
	// 	"https://readnovelfull.com/lets-manage-the-tower/prologue.html",
	// 	"https://w21.read-onepiece.net/manga/one-piece-chapter-1022/",
	// }

	// for _, url := range items {
	// 	if _, err := sqlDB.ExecContext(ctx, `
	// 		INSERT OR IGNORE INTO items (user_id, url, added_ts)
	// 		VALUES (1, ?, ?)
	// 	`, url, now); err != nil {
	// 		return err
	// 	}
	// }

	return nil
}
