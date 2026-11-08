// Static on-device mDNS diagnostic. Prints only public service names, ports and endpoint paths.
package main

import (
	"context"
	"fmt"
	"time"

	zeroconf "github.com/libp2p/zeroconf/v2"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	found := make(chan *zeroconf.ServiceEntry, 16)
	go func() {
		if err := zeroconf.Browse(ctx, "_spotify-connect._tcp", "local.", found); err != nil {
			fmt.Println("mDNS lookup failed")
			cancel()
		}
	}()
	for {
		select {
		case e, ok := <-found:
			if !ok {
				return
			}
			fmt.Printf("Spotify mDNS: instance=%q port=%d IPv4=%v TXT=%v\n", e.Instance, e.Port, e.AddrIPv4, e.Text)
		case <-ctx.Done():
			return
		}
	}
}
