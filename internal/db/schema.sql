CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username VARCHAR(255) NOT NULL UNIQUE,
    password VARCHAR(255) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    active_item_id INTEGER NULL,
    FOREIGN KEY(active_item_id) REFERENCES items(id) ON DELETE SET NULL
);

CREATE TABLE items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    title TEXT NULL,
    url TEXT NOT NULL,
    added_ts INTEGER NOT NULL,
    read_ts INTEGER NULL,
    uploaded_html_brotli BLOB NULL,
    UNIQUE(user_id, url),
    FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TRIGGER update_active_item_on_delete
AFTER DELETE ON items
FOR EACH ROW
BEGIN
    UPDATE users
    SET active_item_id = (
        SELECT id FROM items
        WHERE user_id = users.id
        AND read_ts IS NOT NULL
        ORDER BY read_ts DESC
        LIMIT 1
    )
    WHERE active_item_id = OLD.id;
END;
