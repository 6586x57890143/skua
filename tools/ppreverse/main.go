// Command ppreverse undoes a joinder paid wrong, and everything its
// signers bought since, through pp.Reverse. Stop the bot first: it holds
// /pp in memory and would write over the result.
//
//	SKUA_DATABASE_URL=... go run ./tools/ppreverse -guild <id> -from <seq> -users <id,id,...>
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/modules/pp"
	"github.com/6586x57890143/skua/internal/store"
)

func main() {
	guild := flag.String("guild", "", "the server")
	from := flag.Int64("from", 0, "the ledger seq the bad joinder starts at")
	list := flag.String("users", "", "its signers, comma separated")
	flag.Parse()
	g, err := snowflake.Parse(*guild)
	if err != nil || *from <= 0 || *list == "" {
		log.Fatal("ppreverse: -guild, -from and -users are all needed")
	}
	var users []snowflake.ID
	for _, s := range strings.Split(*list, ",") {
		u, err := snowflake.Parse(strings.TrimSpace(s))
		if err != nil {
			log.Fatalf("ppreverse: %q: %v", s, err)
		}
		users = append(users, u)
	}
	ctx := context.Background()
	db, err := store.Open(ctx, os.Getenv("SKUA_DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	out, err := pp.Reverse(ctx, db, g, *from, users)
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range out {
		fmt.Printf("%d: took back %d, refunded %d, rung %d to %d, livery faded %v, paid %d (hundredths of a pp)\n",
			r.User, r.TookBack, r.Refunded, r.RungFrom, r.RungTo, r.Faded, r.Paid)
	}
}
