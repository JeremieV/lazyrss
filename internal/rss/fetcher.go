package rss

import (
	"clirss/internal/db"
	"context"
	"time"

	"github.com/mmcdole/gofeed"
)

func FetchFeed(url string) (*gofeed.Feed, error) {
	fp := gofeed.NewParser()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return fp.ParseURLWithContext(url, ctx)
}

func SyncFeed(feedID int64, url string) error {
	f, err := FetchFeed(url)
	if err != nil {
		return err
	}

	var entries []db.Entry
	for _, item := range f.Items {
		publishedAt := time.Now()
		if item.PublishedParsed != nil {
			publishedAt = *item.PublishedParsed
		} else if item.UpdatedParsed != nil {
			publishedAt = *item.UpdatedParsed
		}

		entries = append(entries, db.Entry{
			Title:       item.Title,
			Link:        item.Link,
			Description: item.Description,
			Content:     item.Content,
			PublishedAt: publishedAt,
		})
	}

	return db.SaveEntries(feedID, entries)
}

