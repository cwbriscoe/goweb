package job

import (
	"github.com/goccy/go-json"
	"github.com/jackc/pgx/v5"
)

// GetParm retrieves the current jobs parm with the given key and sequence
func (e *Entry) GetParm(key string, seq int, val any) error {
	sql := "select data from job.parm where job = $1 and key = $2 and seq = $3;"

	var p any
	err := e.DB.QueryRow(e.Ctx, sql, e.NameKey, key, seq).Scan(&p)

	if err != nil && err != pgx.ErrNoRows {
		return err
	}

	jsonStr, err := json.Marshal(p)
	if err != nil {
		return err
	}

	return json.Unmarshal(jsonStr, val)
}

// SetParm sets the current jobs parm with the given key and sequence
func (e *Entry) SetParm(key string, seq int, p any) error {
	sql := "update job.parm set data = $4 where job = $1 and key = $2 and seq = $3;"
	tag, err := e.DB.Exec(e.Ctx, sql, e.NameKey, key, seq, p)
	if err != nil {
		return err
	}

	if tag.RowsAffected() > 0 {
		return nil
	}

	sql = "insert into job.parm values ($1, $2, $3, $4);"
	_, err = e.DB.Exec(e.Ctx, sql, e.NameKey, key, seq, p)
	if err != nil {
		return err
	}

	return nil
}
