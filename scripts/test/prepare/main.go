package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	clickhouse "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/unifiedmessagelog"
)

func main() {
	if err := prepare(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func prepare() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	options, err := clickhouse.ParseDSN(os.Getenv("TALGENT_TEST_CLICKHOUSE_DSN"))
	if err != nil {
		return err
	}
	conn, err := clickhouse.Open(options)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := unifiedmessagelog.ApplySchema(ctx, conn); err != nil {
		return err
	}
	schema, err := os.ReadFile("deploy/middleware/init/clickhouse/002-talgent.sql")
	if err != nil {
		return err
	}
	for _, statement := range strings.Split(string(schema), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := conn.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
