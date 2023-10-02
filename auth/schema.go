// Copyright 2023 Christopher Briscoe.  All rights reserved.

package auth

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// CreateSchema will create the auth schema and associated tables needed
// for this package to run
func CreateSchema(ctx context.Context, conn *pgx.Conn) error {
	var sql string
	var err error

	sql = "drop schema if exists auth cascade;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = "create schema auth authorization current_role;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = `
CREATE TABLE auth.user (
	id int4 NOT NULL GENERATED ALWAYS AS IDENTITY( INCREMENT BY 1 MINVALUE 1 MAXVALUE 2147483647 START 1 CACHE 1 NO CYCLE),
	"name" varchar NOT NULL,
	lname varchar NOT NULL,
	email varchar NOT NULL,
	hash varchar NOT NULL,
	roles _text NOT NULL,
	last_login_ts timestamptz NOT NULL,
	create_ts timestamptz NOT NULL,
	CONSTRAINT auth_pk PRIMARY KEY (id)
);
CREATE UNIQUE INDEX auth_email_idx ON auth.user USING btree (email);
CREATE UNIQUE INDEX auth_lname_idx ON auth.user USING btree (lname);
CREATE UNIQUE INDEX auth_name_idx ON auth.user USING btree (name);`
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = "grant select, insert, update on table auth.user to api;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = `
	CREATE TABLE auth.sess (
		id int4 NOT NULL,
		auth_id int4 NOT NULL,
		create_ts timestamptz NOT NULL,
		expire_ts timestamptz NOT NULL,
		last_used_ts timestamptz NOT NULL,
		CONSTRAINT sess_pk PRIMARY KEY (id, auth_id)
	);`
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = "grant select, insert, update, delete on table auth.sess to api;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	sql = "ALTER TABLE auth.sess ADD CONSTRAINT sess_fk FOREIGN KEY (auth_id) REFERENCES auth.user(id) ON DELETE CASCADE;"
	_, err = conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	return nil
}
