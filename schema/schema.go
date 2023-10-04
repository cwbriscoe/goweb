// Copyright 2023 Christopher Briscoe.  All rights reserved.
package schema

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/cwbriscoe/goutil/db"
	"github.com/cwbriscoe/goweb/auth"
	"github.com/cwbriscoe/goweb/job"
	"github.com/jackc/pgx/v5"
)

var connInfo *db.PgConnInfo

// CreateDatabase creates a new database and renames old one if it already exists
func CreateDatabase(name string) (*pgx.Conn, error) {
	var err error
	connInfo, err = parseFlags()
	if err != nil {
		return nil, err
	}

	conn, err := db.GetPgConn(connInfo)
	if err != nil {
		return nil, err
	}

	return createSchema(context.Background(), conn, name)
}

func parseFlags() (*db.PgConnInfo, error) {
	// parse flags
	host := flag.String("host", "localhost", "database host")
	port := flag.String("port", "5432", "database port")
	name := flag.String("name", "postgres", "database name")
	user := flag.String("user", "postgres", "database user")
	pass := flag.String("pass", "postgres", "database password")

	flag.Parse()

	if *name == "" {
		return nil, errors.New("a database name must be provided (-name)")
	}

	if *pass == "" {
		return nil, errors.New("a database password must be provided (-pass)")
	}

	return &db.PgConnInfo{
		Host: *host,
		Port: *port,
		Name: *name,
		User: *user,
		Pass: *pass,
	}, nil
}

func createSchema(ctx context.Context, conn *pgx.Conn, name string) (*pgx.Conn, error) {
	var nm string

	row := conn.QueryRow(ctx, "select datname from pg_database where datname = $1;", name)
	err := row.Scan(&nm)
	if err != nil && err != pgx.ErrNoRows {
		return nil, err
	}

	exists := (err != pgx.ErrNoRows)

	if exists {
		err = renameDatabase(ctx, conn, name)
		if err != nil {
			return nil, err
		}
	}

	err = createNewDatabase(ctx, conn, name)
	if err != nil {
		return nil, err
	}

	err = CreateRole(ctx, conn, "api")
	if err != nil {
		return nil, err
	}

	err = CreateRole(ctx, conn, "job")
	if err != nil {
		return nil, err
	}

	connInfo.Name = name
	fmt.Println("connecting to", name)
	conn, err = db.GetPgConn(connInfo)
	if err != nil {
		return nil, err
	}

	fmt.Println("creating auth schema")
	err = auth.CreateSchema(ctx, conn)
	if err != nil {
		return nil, err
	}

	fmt.Println("creating job schema")
	err = job.CreateSchema(ctx, conn)
	if err != nil {
		return nil, err
	}

	fmt.Println("successfully created database", name, "base schema")
	return conn, nil
}

func renameDatabase(ctx context.Context, conn *pgx.Conn, name string) error {
	now := time.Now()
	newName := name + now.Format("20060102150405")

	fmt.Println("renaming database", name, "to", newName)

	sql := "alter database " + name + " rename to " + newName
	_, err := conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	return nil
}

func createNewDatabase(ctx context.Context, conn *pgx.Conn, name string) error {
	fmt.Println("creating database", name)

	sql := "create database " + name + " template template0;"
	_, err := conn.Exec(ctx, sql)
	if err != nil {
		return err
	}

	return nil
}

// CreateRole creates a role with only login permissions
func CreateRole(ctx context.Context, conn *pgx.Conn, name string) error {
	fmt.Println("attempting to create role", name)

	sql := "select 'create role " + name + " with login password ''" + name + "'';'"
	sql += "where not exists (select from pg_catalog.pg_roles where rolname = '" + name + "');"

	var str string
	row := conn.QueryRow(ctx, sql)
	err := row.Scan(&str)
	if err == pgx.ErrNoRows {
		fmt.Println("role", name, "already exists")
		return nil
	}

	if err != nil {
		return err
	}

	fmt.Println("creating role", name)
	_, err = conn.Exec(ctx, str)
	if err != nil {
		return err
	}

	return nil
}
