-- name: UsersAdd :one
INSERT INTO users (username, password) VALUES (?, ?) RETURNING id;

-- name: UsersGetActiveItem :one
SELECT i.* FROM items i
JOIN users u ON u.active_item_id = i.id
WHERE u.id = ? LIMIT 1;

-- name: UsersGetPassword :one
SELECT password FROM users WHERE username = ?;

-- name: UsersGetByName :one
SELECT * FROM users WHERE username = ?;

-- name: UsersOwnsItem :one
SELECT EXISTS(
    SELECT 1 FROM users u
    JOIN items i ON u.id = i.user_id
    WHERE u.username = ? AND i.id = ?
);

-- name: UsersSetActiveItem :exec
UPDATE users
SET active_item_id = ?
WHERE id = ?;

-----------------------------

-- name: ItemsListPerUser :many
SELECT * FROM items
WHERE user_id = ?
ORDER BY added_ts DESC;

-- name: ItemsAdd :one
INSERT INTO items (
  user_id, url, added_ts
) VALUES (
  ?, ?, ?
)
ON CONFLICT(user_id, url) DO UPDATE SET user_id = excluded.user_id
RETURNING id;

-- name: ItemsDelete :exec
DELETE FROM items
WHERE id = ?;

-- name: ItemsGet :one
SELECT * FROM items
WHERE id = ? LIMIT 1;

-- name: ItemsGetUrlSetRead :one
UPDATE items
SET read_ts = ?
WHERE id = ?
RETURNING url;

-- name: ItemsUpdateTitle :one
UPDATE items
SET title = ?
WHERE id = ?
RETURNING *;

-- name: ItemsSetUrl :exec
UPDATE items
SET url = ?
WHERE id = ?;

-- name: ItemsAddWithUploadedContent :one
INSERT INTO items (
  user_id, title, url, added_ts, uploaded_html_brotli
) VALUES (
  ?, ?, ?, ?, ?
)
ON CONFLICT(user_id, url) DO UPDATE SET
  user_id = excluded.user_id,
  uploaded_html_brotli = excluded.uploaded_html_brotli
RETURNING id;
