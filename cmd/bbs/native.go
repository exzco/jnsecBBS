package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"github.com/exzco/bbs/internal/migration"
)

// nativeImportCmd is intentionally separate from the live application's DSN.
// The native schema is a shadow migration target, not a serving database yet.
func nativeImportCmd(args []string) {
	fs := flag.NewFlagSet("native-import", flag.ExitOnError)
	initialize := fs.Bool("initialize", false, "initialize an empty native target")
	readOnly := fs.Bool("read-only-target", false, "acknowledge native target has no forum writers")
	fs.Parse(args) // ExitOnError：解析失败时由 flag 包直接退出
	if fs.NArg() != 0 {
		log.Fatal("native-import accepts flags only; pass DSNs through environment variables")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result, err := migration.Run(ctx, migration.Config{
		SourceDSN:  os.Getenv("JNSEC_IMPORT_SOURCE_DSN"),
		TargetDSN:  os.Getenv("JNSEC_IMPORT_TARGET_DSN"),
		Initialize: *initialize, ReadOnlyTarget: *readOnly,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		log.Fatal("could not write native import summary")
	}
}
