// Command join-once attempts one game join using the same wire clients as the
// game bot suite. The caller owns retries and measures the whole cold start.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/minecraft-java/minecraftproto"
	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
	"github.com/GameplanePanel/gameplane/test/e2e/internal/terraria/terrariaproto"
)

func main() {
	game := flag.String("game", "", "minecraft-java or terraria")
	addr := flag.String("addr", "", "published game host:port")
	user := flag.String("user", "gp-wake", "offline Minecraft player name")
	timeout := flag.Duration("deadline", 20*time.Second, "deadline for this attempt")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	v := join(ctx, *game, *addr, *user)
	cancel()
	line, err := v.Encode()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(line)
	os.Exit(joindepth.ExitCodeFromVerdict(&v, joindepth.JOINED, false))
}

func join(ctx context.Context, game, addr, user string) joindepth.ProbeVerdict {
	v := joindepth.ProbeVerdict{ReachedDepth: joindepth.JoinDepth(-1)}
	switch game {
	case "minecraft-java":
		status, result, err := minecraftproto.Connect(ctx, addr, user)
		if status != nil {
			v.ReachedDepth = joindepth.QUERY
		}
		if err != nil {
			v.Err = err
			return v
		}
		switch result.Outcome {
		case minecraftproto.Success:
			v.ReachedDepth = joindepth.JOINED
			v.Detail = fmt.Sprintf("Minecraft Login Success (0x02) for %s", user)
		case minecraftproto.NeedsAuth:
			v.ReachedDepth = joindepth.PARTIAL
			v.Detail = "Minecraft Encryption Request requires an authenticated client"
		default:
			v.Detail = "Minecraft login refused"
		}
	case "terraria":
		conn, handshake, err := terrariaproto.Connect(ctx, addr)
		if err != nil {
			if errors.Is(err, terrariaproto.ErrPasswordRequired) {
				v.ReachedDepth = joindepth.PARTIAL
				v.Detail = "Terraria password prompt"
			}
			v.Err = err
			return v
		}
		defer func() { _ = conn.Close() }()
		v.ReachedDepth = joindepth.PARTIAL
		if err := conn.RequestWorldData(ctx); err != nil {
			v.Err = err
			return v
		}
		v.ReachedDepth = joindepth.JOINED
		v.Detail = fmt.Sprintf("Terraria WorldData (7) after assignment of player slot %d", handshake.Slot)
	default:
		v.Err = errors.New("unsupported game; use an entitled game client adapter")
	}
	return v
}
