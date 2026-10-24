//go:build !panel

package bluetooth

import (
	"context"

	esphome "github.com/ygelfand/go-esphome-device"
	"google.golang.org/protobuf/proto"
)

func (b *Proxy) gattDisable()    {}
func (b *Proxy) gattProbeOwner() {}

func (*Proxy) gattFeatures() esphome.BluetoothFeature { return 0 }
func (*Proxy) gattHandle(context.Context, *esphome.Conn, proto.Message) (bool, error) {
	return false, nil
}
