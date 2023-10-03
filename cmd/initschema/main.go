// Copyright 2023 Christopher Briscoe.  All rights reserved.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/cwbriscoe/goutil/db"
	"github.com/jackc/pgx/v5"
)

func main() {
	err := runSchema()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	os.Exit(0)
}

func runSchema() error {
	// parse flags
	host := flag.String("host", "localhost", "database host")
	port := flag.String("port", "5432", "database port")
	name := flag.String("name", "", "database name")
	user := flag.String("user", "postgres", "database user")
	pass := flag.String("pass", "", "database password")

	flag.Parse()

	if *name == "" {
		return errors.New("a database name must be provided (-name)")
	}

	if *pass == "" {
		return errors.New("a database password must be provided (-pass)")
	}

	conn, err := db.GetPgConn(db.PgConnInfo{
		Host: *host,
		Port: *port,
		Name: *name,
		User: *user,
		Pass: *pass,
	})
	if err != nil {
		return err
	}

	return updateSchema(context.Background(), conn)
}

func updateSchema(ctx context.Context, conn *pgx.Conn) error {
	var cnt int

	row := conn.QueryRow(ctx, "select count(*) from auth.user;")
	err := row.Scan(&cnt)
	if err != nil {
		return err
	}

	fmt.Println("result is:", cnt)

	return nil
}
