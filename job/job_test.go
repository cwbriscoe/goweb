// Copyright 2023 Christopher Briscoe.  All rights reserved.

package job

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

/*
1.  first you want to create a postgres database if not already created:
			createdb -T template0 goweb

2.  second you must create the following role in postgres (change as needed):
  		create role job login password 'job';

3.  third you need to create an GOWEBDB environment variable with a postgres conn string:
			export GOWEBDB=postgresql://localhost:5432/goweb?user=<user>&password=<pass>
*/

var conn *pgx.Conn

// TestMain will completely delete and recreate the auth schema
// in the database pointed to by the GOWEBDB environment variable
func TestMain(m *testing.M) {
	var err error
	ctx := context.Background()

	conn, err = pgx.Connect(ctx, os.Getenv("GOWEBDB"))
	if err != nil {
		fmt.Println("error connecting to database:")
		fmt.Println(err.Error())
		os.Exit(1)
	}

	err = CreateSchema(ctx, conn)
	if err != nil {
		fmt.Println("error creating schema:")
		fmt.Println(err.Error())
		os.Exit(1)
	}

	os.Exit(m.Run())
}
