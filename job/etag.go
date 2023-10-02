// Copyright 2023 Christopher Briscoe.  All rights reserved.

package job

import (
	"net/url"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/cwbriscoe/goutil/str"
	"github.com/jackc/pgx/v5"
)

// GetEtag retrieve the last known etag for the provided url.
func (e *Entry) GetEtag(nurl *url.URL) (string, error) {
	path := nurl.RequestURI()
	id := int64(xxhash.Sum64String(path))

	sql := "select etag from job.etag where id = $1;"

	var etag string
	err := e.DB.QueryRow(e.Ctx, sql, id).Scan(&etag)

	if err != nil && err != pgx.ErrNoRows {
		return "", err
	}

	return etag, nil
}

// SetEtag records the etag for the provided url.
func (e *Entry) SetEtag(nurl *url.URL, etag string) error {
	if etag == "" {
		return nil
	}

	path := nurl.RequestURI()
	id := int64(xxhash.Sum64String(path))

	etag = str.TrimQuotes(strings.TrimPrefix(etag, "W/"))

	sql := "insert into job.etag values ($1, $2, now()) on conflict (id) do update set etag = $2, last_update_ts = now();"
	_, err := e.DB.Exec(e.Ctx, sql, id, etag)

	return err
}
