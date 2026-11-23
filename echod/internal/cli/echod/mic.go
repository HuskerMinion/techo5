package echod

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/audio"
)

func newMicCmd() *cobra.Command {
	var (
		card, device    int
		chans, rate     int
		period, periods int
		secs            float64
		rawPath         string
		direct          bool
	)

	c := &cobra.Command{
		Use:   "mic",
		Short: "Capture from the mic array and report per-channel levels",
		Long: "Captures from the microphones and reports peak, RMS, and the margin over what a\n" +
			"24->16 bit truncation would discard.\n\n" +
			"By default the capture is the board's own, as the daemon has it: frames of\n" +
			"S24_3LE at 16 kHz, the microphones first and then the playback\n" +
			"reference, which is silent unless audio is playing. How many of each there are\n" +
			"is the board's; the first line printed says.\n\n" +
			"--direct, or any of --card, --device, --channels, --rate, --period and\n" +
			"--periods, opens that ALSA device as S24_3LE yourself instead.\n\n" +
			"The daemon holds the capture device, so stop it first or this will block\n" +
			"rather than fail.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A device or format asked for is a direct capture: the board's own would ignore it.
			for _, f := range []string{"card", "device", "channels", "rate", "period", "periods"} {
				direct = direct || cmd.Flags().Changed(f)
			}
			if !direct {
				return boardMic(cmd, secs, rawPath)
			}
			pcm, err := alsa.Open(card, device, alsa.Config{
				Channels:   chans,
				Rate:       rate,
				Format:     alsa.FormatS24_3LE,
				Bits:       24,
				PeriodSize: period,
				Periods:    periods,
			})
			if err != nil {
				return err
			}
			defer func() { _ = pcm.Close() }()

			var raw *os.File
			if rawPath != "" {
				if raw, err = os.Create(rawPath); err != nil {
					return err
				}
				defer func() { _ = raw.Close() }()
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "capturing %.1fs: %d ch @ %d Hz, S24_3LE, period %d x %d\n",
				secs, chans, rate, period, periods)

			st := make([]audio.Stats, chans)
			buf := make([]byte, period*pcm.FrameBytes())
			deadline := time.Now().Add(time.Duration(secs * float64(time.Second)))
			overruns := 0

			for time.Now().Before(deadline) {
				n, err := pcm.Read(buf)
				if err == alsa.ErrOverrun {
					overruns++
					continue
				}
				if err != nil {
					return err
				}
				if raw != nil {
					if _, err := raw.Write(buf[:n]); err != nil {
						return err
					}
				}
				fb := pcm.FrameBytes()
				for off := 0; off+fb <= n; off += fb {
					for ch := 0; ch < chans; ch++ {
						st[ch].Add(audio.DecodeS24LE3(buf[off+ch*3:]))
					}
				}
			}

			if overruns > 0 {
				fmt.Fprintf(out, "overruns: %d\n", overruns)
			}
			audio.WriteReport(out, st)
			return nil
		},
	}

	c.Flags().IntVarP(&card, "card", "D", 0, "ALSA card (implies --direct)")
	c.Flags().IntVarP(&device, "device", "d", mic.CaptureDevice, "ALSA capture device (implies --direct)")
	c.Flags().IntVarP(&chans, "channels", "c", mic.Channels, "channels (implies --direct)")
	c.Flags().IntVarP(&rate, "rate", "r", 16000, "sample rate (implies --direct)")
	c.Flags().IntVarP(&period, "period", "p", mic.FrameSamples, "period size in frames (implies --direct)")
	c.Flags().IntVarP(&periods, "periods", "n", 8, "period count (implies --direct)")
	c.Flags().Float64VarP(&secs, "seconds", "t", 5, "capture duration")
	c.Flags().StringVar(&rawPath, "raw", "", "also write raw interleaved S24_3LE here")
	c.Flags().BoolVar(&direct, "direct", false, "open --card/--device as S24_3LE at --rate yourself, rather than through the board's own "+
		"capture (which on the Dot 3, the Dot 5 and the 2nd gen Show 8 converts another format to it)")
	return c
}

// boardMic is the capture as the daemon has it (mic.Acquire): whole frames of mic.Channels at mic.Rate in
// S24_3LE whatever the hardware runs at, so the boards whose converter is opened at 96 kHz in 32 bits and
// decimated (athena.go) report the same way as the Dot 2, whose hardware gives that format as it is.
func boardMic(cmd *cobra.Command, secs float64, rawPath string) error {
	source, err := mic.Acquire()
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()

	var raw *os.File
	if rawPath != "" {
		if raw, err = os.Create(rawPath); err != nil {
			return err
		}
		defer func() { _ = raw.Close() }()
	}

	ctx, stop := context.WithCancel(cmd.Context())
	defer stop()
	frames, done := source.ListenRaw()
	defer done()
	go func() { _ = source.Run(ctx) }()

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "capturing %.1fs through the board's capture: %d ch @ %d Hz, S24_3LE (ch0-ch%d microphones, then the reference)\n",
		secs, mic.Channels, mic.Rate, mic.Mics-1)
	st := make([]audio.Stats, mic.Channels)
	fb := mic.Channels * 3
	deadline := time.After(time.Duration(secs * float64(time.Second)))
	for {
		select {
		case <-deadline:
			audio.WriteReport(out, st)
			return nil
		case buf, ok := <-frames:
			if !ok {
				return fmt.Errorf("the capture stopped")
			}
			if raw != nil {
				if _, err := raw.Write(buf); err != nil {
					return err
				}
			}
			for off := 0; off+fb <= len(buf); off += fb {
				for ch := range mic.Channels {
					st[ch].Add(audio.DecodeS24LE3(buf[off+ch*3:]))
				}
			}
		}
	}
}
