// Copyright 2023 Christopher Briscoe.  All rights reserved.

package job

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// CreateSchema will create the job schema and associated tables needed
// for this package to run
func CreateSchema(ctx context.Context, conn *pgx.Conn) error {
	var sql string
	var err error

	sql = "drop schema if exists job cascade;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = "create schema job authorization current_role;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = `
	CREATE TABLE job.entry (
		job_id int4 NOT NULL,
		"name" varchar NOT NULL,
		"function" varchar NOT NULL,
		"every" interval NOT NULL,
		priority int4 NOT NULL,
		enabled bool NOT NULL,
		"exclusive" bool NOT NULL,
		multiple bool NOT NULL,
		last_run_ts timestamptz NOT NULL,
		CONSTRAINT entry_pk PRIMARY KEY (job_id)
	); `
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = "grant select, update on table job.entry to job;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = `
	CREATE TABLE job.active (
		run_id int4 NOT NULL GENERATED ALWAYS AS IDENTITY( INCREMENT BY 1 MINVALUE 1 MAXVALUE 2147483647 START 1 CACHE 1 NO CYCLE),
		job_id int4 NOT NULL,
		start_ts timestamptz NOT NULL,
		CONSTRAINT active_pk PRIMARY KEY (run_id)
	);`
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = "grant select, insert, update, delete on table job.active to job;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = "alter table job.active add constraint active_fk foreign key (job_id) references job.entry(job_id) on delete cascade;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = `
	CREATE TABLE job.completed (
		run_id int4 NOT NULL,
		job_id int4 NOT NULL,
		start_ts timestamptz NOT NULL,
		finish_ts timestamptz NOT NULL,
		status varchar NOT NULL,
		CONSTRAINT completed_pk PRIMARY KEY (run_id)
	); `
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = "grant select, insert, update, delete on table job.completed to job;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = "alter table job.completed add constraint completed_fk foreign key (job_id) references job.entry(job_id) on delete cascade;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = `
	CREATE TABLE job.parm (
		job varchar NOT NULL,
		"key" varchar NOT NULL,
		seq int4 NOT NULL,
		"data" jsonb NOT NULL,
		CONSTRAINT parm_pk PRIMARY KEY (job, key, seq)
	);`
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = "grant select, insert, update, delete on table job.parm to job;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = `
	CREATE TABLE job.etag (
		id int8 NOT NULL,
		etag varchar NOT NULL,
		last_update_ts timestamptz NOT NULL,
		CONSTRAINT etag_pk PRIMARY KEY (id)
	);`
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = "grant select, insert, update, delete on table job.etag to job;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	return nil
}
