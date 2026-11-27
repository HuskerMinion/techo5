package diag

import (
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// flashEvery is how often the data written is published. The count moves on every sample, so at a
// short Metrics interval it would be a new state in Home Assistant's recorder each time; its
// statistics are kept by the hour, and every ten minutes is plenty to make a day's writes from.
const flashEvery = 10 * time.Minute

// flash is what the eMMC says about its own wear (#771): it is the part of an old Echo that wears out
// first, and until now the chip's estimate could only be read over SSH. Only reads, from sysfs; nothing
// here writes to the flash it is measuring.
type flash struct {
	reader metrics.Reader
	dev    string

	// Each is nil where the chip has nothing to say: no eMMC found, no file (an app on a tablet may
	// not see /sys/block), or an eMMC older than 5.0 that leaves the values undefined. A sensor that
	// could never have a value is not offered.
	life    *esphome.Sensor
	spare   *esphome.TextSensor
	written *esphome.Sensor

	writtenAt time.Time
}

// spareBlocks is the pre-end-of-life status as Home Assistant shows it.
var spareBlocks = map[int]string{1: "Normal", 2: "Warning", 3: "Urgent"}

// newFlash finds the eMMC and builds its sensors. Life used and spare blocks are set here once: the
// kernel reads them from the chip when it finds the card, so they cannot change before the next boot.
func newFlash(r metrics.Reader) *flash {
	f := &flash{reader: r, dev: r.Flash()}
	if f.dev == "" {
		slog.Info("no eMMC found; no flash wear sensors")
		return f
	}

	if life := r.FlashLifeUsed(f.dev); life.Known {
		// A percent, the top of the chip's ten-percent band, rather than the band as text: a number can
		// be graphed and alerted on (above 100 is past the rated life).
		f.life = &esphome.Sensor{
			Base: esphome.Base{
				ObjectID: "flash_life_used", Name: "Flash life used", Icon: "mdi:chip",
				Category: esphome.CategoryDiagnostic,
			},
			Unit:       "%",
			StateClass: esphome.StateClassMeasurement,
		}
		f.life.Set(float32(life.Value))
	}

	if level, ok := spareBlocks[r.FlashSpareBlocks(f.dev)]; ok {
		f.spare = &esphome.TextSensor{
			Base: esphome.Base{
				ObjectID: "flash_spare_blocks", Name: "Flash spare blocks", Icon: "mdi:chip",
				Category: esphome.CategoryDiagnostic,
			},
		}
		f.spare.Set(level)
	}

	if r.FlashWritten(f.dev).Known {
		// In MiB, which Home Assistant knows as a data size and converts for display: the stat counts
		// sectors, and 2048 of them are a MiB, the figure the daily-writes comparisons are made in. A
		// float32 holds that to a tenth of a MiB well past what a device writes between boots. Total
		// increasing, so the drop at a reboot starts a new cycle and the statistics give the writes per
		// day.
		f.written = &esphome.Sensor{
			Base: esphome.Base{
				ObjectID: "flash_data_written", Name: "Flash data written", Icon: "mdi:content-save-edit",
				Category: esphome.CategoryDiagnostic,
			},
			Unit:        "MiB",
			DeviceClass: "data_size",
			StateClass:  esphome.StateClassTotalIncreasing,
			Decimals:    1,
		}
	}

	slog.Info("eMMC", "device", f.dev, "life_used", r.FlashLifeUsed(f.dev).Value, "spare_blocks", r.FlashSpareBlocks(f.dev))
	return f
}

// entities is the sensors this chip has values for.
func (f *flash) entities() []esphome.Entity {
	var out []esphome.Entity
	if f.life != nil {
		out = append(out, f.life)
	}
	if f.spare != nil {
		out = append(out, f.spare)
	}
	if f.written != nil {
		out = append(out, f.written)
	}
	return out
}

// sample publishes the data written, at most every flashEvery. The first sample after a start
// publishes at once, so the sensor has a value before Home Assistant asks.
func (f *flash) sample(now time.Time) {
	if f.written == nil {
		return
	}
	if !f.writtenAt.IsZero() && now.Sub(f.writtenAt) < flashEvery {
		return
	}
	w := f.reader.FlashWritten(f.dev)
	if !w.Known {
		return
	}
	f.written.Set(float32(w.Value / (1 << 20)))
	f.writtenAt = now
}
