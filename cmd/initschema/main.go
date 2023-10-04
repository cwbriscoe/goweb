// Copyright 2023 Christopher Briscoe.  All rights reserved.
package main

import (
	"fmt"
	"os"

	"github.com/cwbriscoe/goweb/schema"
)

func main() {
	_, err := schema.CreateDatabase("goweb")
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}
