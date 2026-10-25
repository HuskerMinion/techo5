//go:build !dot

package ambient

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/i2c"
)

// The Skylight Calendar's light sensor: an Everlight EM3071X at 0x24 on I2C1, "ls_em3071x" in the
// stock device tree. No kernel driver for it in TECHO5's mainline kernel, so it is read through
// /dev/i2c-1 (CONFIG_I2C_CHARDEV). The registers are the ones Rockchip's GPL driver uses
// (drivers/input/sensors/lsensor/ls_em3071x.c): the command register switches the light half on and
// the reading is twelve bits in two registers, low byte first.
const (
	emBus  = 1
	emAddr = 0x24

	emCmd   = 0x01 // ALS_CMD
	emData1 = 0x09 // ALS_DT1: the low eight bits
	emData2 = 0x0A // ALS_DT2: the high four
	emReset = 0x0E // SW_RESET

	// emOn is what Rockchip's driver sets in ALS_CMD to switch the light sensor on, from zero.
	emOn = 0x06
)

// emLuxPerCount turns a reading into lux, measured on the unit against a phone's light meter held
// beside it: 162 at 116 lux (0.72) and 15 at 8.7 lux (0.58).
var emLuxPerCount = 0.7

type em3071x struct {
	bus *i2c.Bus
}

// openEM3071X switches the sensor on and waits out its first integration.
func openEM3071X() (*em3071x, error) {
	bus, err := i2c.Open(emBus, emAddr, false)
	if err != nil {
		return nil, err
	}
	if err := bus.WriteReg(emReset, 0x00); err != nil {
		bus.Close()
		return nil, fmt.Errorf("em3071x: %w", err)
	}
	if err := bus.WriteReg(emCmd, emOn); err != nil {
		bus.Close()
		return nil, fmt.Errorf("em3071x: switching it on: %w", err)
	}
	time.Sleep(250 * time.Millisecond)
	return &em3071x{bus: bus}, nil
}

// count is the raw reading, 0..4095.
func (e *em3071x) count() (int, error) {
	lo, err := e.bus.ReadReg(emData1)
	if err != nil {
		return 0, err
	}
	hi, err := e.bus.ReadReg(emData2)
	if err != nil {
		return 0, err
	}
	return emCount(lo, hi), nil
}

// emCount puts the two registers together: twelve bits, the high four in the second's low nibble.
func emCount(lo, hi uint8) int { return int(hi&0x0F)<<8 | int(lo) }

func (e *em3071x) sample() (float64, bool, error) {
	n, err := e.count()
	if err != nil {
		return 0, false, err
	}
	return float64(n) * emLuxPerCount, true, nil
}

func (e *em3071x) close() error {
	_ = e.bus.WriteReg(emCmd, 0x00) // off again
	return e.bus.Close()
}

// startEM3071X opens the Skylight's sensor.
func (s *Sensor) startEM3071X() error {
	e, err := openEM3071X()
	if err != nil {
		return fmt.Errorf("ambient: %w", err)
	}
	s.mu.Lock()
	s.poll = e
	s.mu.Unlock()
	n, _ := e.count()
	slog.Info("light sensor on", "device", fmt.Sprintf("/dev/i2c-%d 0x%02x", emBus, emAddr), "period", period, "count", n)
	return nil
}
