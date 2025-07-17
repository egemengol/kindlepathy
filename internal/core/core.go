package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	badger "github.com/dgraph-io/badger/v4"
	db "github.com/egemengol/kindlepathy/internal/db/generated"
)

type Core struct {
	httpClient        *http.Client
	readabilityClient *ReadabilityClient
	queries           *db.Queries
	Logger            *slog.Logger
	cache             *badger.DB
}

func NewCore(httpClient *http.Client,
	readabilityClient *ReadabilityClient,
	queries *db.Queries,
	logger *slog.Logger,
	cache *badger.DB,
) *Core {
	return &Core{
		httpClient:        httpClient,
		readabilityClient: readabilityClient,
		queries:           queries,
		Logger:            logger,
		cache:             cache,
	}
}

func (c *Core) AddItem(ctx context.Context, userID int64, rawurl string, now time.Time) (int64, error) {
	if rawurl == "" {
		return 0, fmt.Errorf("url cannot be empty")
	}
	u, err := url.Parse(rawurl)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return 0, fmt.Errorf("invalid url: %w", err)
	}
	return c.queries.ItemsAdd(ctx, db.ItemsAddParams{
		UserID:  userID,
		Url:     rawurl,
		AddedTs: now.Unix(),
	})
}

func (c *Core) AddItemWithTitleSetActive(ctx context.Context, userID int64, rawurl string, now time.Time) (int64, error) {
	// First add the item
	itemID, err := c.AddItem(ctx, userID, rawurl, now)
	if err != nil {
		return 0, fmt.Errorf("failed to add item: %w", err)
	}

	// Get and clean the content to extract the title
	clean, err := c.getAndCleanCached(ctx, rawurl, "item", 10*time.Minute)
	if err != nil {
		c.Logger.Warn("failed to clean document for title extraction", "error", err, "url", rawurl)
		// Return the item ID even if cleaning fails
		return itemID, nil
	}

	// Update the title
	_, err = c.queries.ItemsUpdateTitle(ctx, db.ItemsUpdateTitleParams{
		Title: clean.Title,
		ID:    itemID,
	})
	if err != nil {
		c.Logger.Warn("failed to update item title", "error", err, "itemID", itemID)
		// Return the item ID even if title update fails
		return itemID, nil
	}

	err = c.queries.UsersSetActiveItem(ctx, db.UsersSetActiveItemParams{
		ActiveItemID: itemID,
		ID:           userID,
	})
	if err != nil {
		c.Logger.Warn("failed to set active item", "error", err, "userID", userID)
	}

	return itemID, nil
}

// AddItemWithUploadedContent adds an item with pre-processed uploaded content
func (c *Core) AddItemWithUploadedContent(ctx context.Context, userID int64, title, rawurl, htmlContent string, now time.Time) (int64, error) {
	if rawurl == "" {
		return 0, fmt.Errorf("url cannot be empty")
	}
	u, err := url.Parse(rawurl)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return 0, fmt.Errorf("invalid url: %w", err)
	}

	// Compress the HTML content
	compressedContent, err := CompressHTML(htmlContent)
	if err != nil {
		return 0, fmt.Errorf("failed to compress content: %w", err)
	}

	itemID, err := c.queries.ItemsAddWithUploadedContent(ctx, db.ItemsAddWithUploadedContentParams{
		UserID:             userID,
		Title:              &title,
		Url:                rawurl,
		AddedTs:            now.Unix(),
		UploadedHtmlBrotli: compressedContent,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to add item with uploaded content: %w", err)
	}

	// Set as active item
	err = c.queries.UsersSetActiveItem(ctx, db.UsersSetActiveItemParams{
		ActiveItemID: itemID,
		ID:           userID,
	})
	if err != nil {
		c.Logger.Warn("failed to set active item", "error", err, "userID", userID)
	}

	return itemID, nil
}

type Item struct {
	ID       int64
	Title    string
	URL      string
	AddedTs  time.Time
	ReadTs   *time.Time
	IsActive bool
}

func (c *Core) ListItems(ctx context.Context, userID int64) ([]Item, error) {
	var activeItemID *int64
	activeItem, err := c.queries.UsersGetActiveItem(ctx, userID)
	if err == nil {
		activeItemID = &activeItem.ID
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to get active item: %w", err)
	}

	items, err := c.queries.ItemsListPerUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	parsed := make([]Item, len(items))
	for i, item := range items {
		var title string
		if item.Title != nil {
			title = item.Title.(string)
		}
		var readTs *time.Time
		if item.ReadTs != nil {
			t := time.Unix(item.ReadTs.(int64), 0)
			readTs = &t
		}
		parsed[i] = Item{
			ID:       item.ID,
			Title:    title,
			URL:      item.Url,
			AddedTs:  time.Unix(item.AddedTs, 0),
			ReadTs:   readTs,
			IsActive: activeItemID != nil && item.ID == *activeItemID,
		}
	}
	return parsed, nil
}

func (c *Core) DeleteItem(ctx context.Context, itemID int64) error {
	return c.queries.ItemsDelete(ctx, itemID)
}

// TODO
func (c *Core) AddUser(ctx context.Context, username string, password string) (int64, error) {
	return c.queries.UsersAdd(ctx, db.UsersAddParams{
		Username: username,
		Password: password,
	})
}

type Clean struct {
	Title       string `json:"title"`
	ContentHTML string `json:"content_html"`
	NavNext     string `json:"nav_next"`
	NavPrev     string `json:"nav_prev"`
}

func (c *Core) getAndClean(ctx context.Context, url string) (*Clean, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GET request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("non-200 response fetching url: %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	body := string(bodyBytes)

	parsed, err := c.readabilityClient.Parse(ctx, body, url)
	if err != nil {
		return nil, fmt.Errorf("failed to parse document: %w", err)
	}

	nav := extractNav(body, url)

	clean := Clean{
		Title:       parsed.Title,
		ContentHTML: parsed.Content,
		NavNext:     nav.Next,
		NavPrev:     nav.Prev,
	}
	c.Logger.Debug("cleaned document", "url", url, "next", nav.Next, "prev", nav.Prev)
	return &clean, nil
}

func (c *Core) getAndCleanCached(ctx context.Context, url string, prefix string, ttl time.Duration) (*Clean, error) {
	cacheKey := fmt.Sprintf("%s:%s", prefix, url)

	if c.cache != nil {
		var cachedClean *Clean
		err := c.cache.View(func(txn *badger.Txn) error {
			item, err := txn.Get([]byte(cacheKey))
			if err != nil {
				return err
			}

			if time.Now().After(time.Unix(int64(item.ExpiresAt()), 0)) {
				return badger.ErrKeyNotFound
			}

			return item.Value(func(val []byte) error {
				return json.Unmarshal(val, &cachedClean)
			})
		})

		if err == nil && cachedClean != nil {
			return cachedClean, nil
		}
	}

	clean, err := c.getAndClean(ctx, url)
	if err != nil {
		return nil, err
	}

	if c.cache != nil {
		cleanBytes, err := json.Marshal(clean)
		if err != nil {
			c.Logger.Warn("failed to marshal clean data for caching", "error", err)
		} else {
			err = c.cache.Update(func(txn *badger.Txn) error {
				entry := badger.NewEntry([]byte(cacheKey), cleanBytes).WithTTL(ttl)
				return txn.SetEntry(entry)
			})
			if err != nil {
				c.Logger.Warn("failed to cache clean data", "error", err, "key", cacheKey)
			}
		}
	}
	return clean, nil
}

func (c *Core) ReadItem(ctx context.Context, itemID int64, now time.Time) (*Clean, error) {
	// First check if this item has uploaded content
	item, err := c.queries.ItemsGet(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("failed to get item: %w", err)
	}

	// Mark as read
	_, err = c.queries.ItemsGetUrlSetRead(ctx, db.ItemsGetUrlSetReadParams{
		ReadTs: now.Unix(),
		ID:     itemID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to mark item as read: %w", err)
	}

	// Check if item has uploaded content
	if item.UploadedHtmlBrotli != nil {
		// Decompress and return uploaded content
		htmlContent, err := DecompressHTML(item.UploadedHtmlBrotli.([]byte))
		if err != nil {
			return nil, fmt.Errorf("failed to decompress uploaded content: %w", err)
		}

		var title string
		if item.Title != nil {
			title = item.Title.(string)
		}

		return &Clean{
			Title:       title,
			ContentHTML: htmlContent,
			NavNext:     "", // No nav for uploaded content
			NavPrev:     "", // No nav for uploaded content
		}, nil
	}

	// Fall back to normal fetch and clean
	clean, err := c.getAndCleanCached(ctx, item.Url, "item", 10*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to clean document: %w", err)
	}

	_, err = c.queries.ItemsUpdateTitle(ctx, db.ItemsUpdateTitleParams{
		Title: clean.Title,
		ID:    itemID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update item title: %w", err)
	}

	return clean, nil
}

func (c *Core) NavigateItem(ctx context.Context, itemID int64, targetPathRel string) error {
	item, err := c.queries.ItemsGet(ctx, itemID)
	if err != nil {
		return fmt.Errorf("failed to get item: %w", err)
	}
	newURL, err := ResolveURL(item.Url, targetPathRel)
	if err != nil {
		return fmt.Errorf("failed to resolve URL: %w", err)
	}
	err = c.queries.ItemsSetUrl(ctx, db.ItemsSetUrlParams{
		Url: newURL,
		ID:  itemID,
	})
	if err != nil {
		return fmt.Errorf("failed to update item: %w", err)
	}
	return nil
}
