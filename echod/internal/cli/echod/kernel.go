package echod

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/HuskerMinion/techo5/echod/internal/feature/busy"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/update/kernel"
)

// newKernelCmd is the kernel update of the units on TECHO5's own mainline kernel (update/kernel): the
// 2nd gen Show 8's boot partition, the Skylight Calendar 15's recovery partition. etc/techo5/boot.sh
// runs it where device.conf says KERNEL_UPDATE: boot early at every boot, settled and then apply once
// the daemon has settled on a good slot, and noroute when a trial boot had everything but a network.
func newKernelCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "kernel",
		Short: "TECHO5's own kernel (2nd gen Show 8, Skylight): the one the root filesystem carries, on trial like a slot",
	}
	paths := func() (kernel.Paths, error) {
		var p kernel.Paths
		switch {
		case layout.AthenaMainline():
			p = kernel.Default
		case layout.Skylight():
			p = kernel.SkylightDefault
		default:
			return kernel.Paths{}, errors.New("only the 2nd gen Show 8 on TECHO5's mainline kernel and the Skylight update their kernel")
		}
		dev, err := kernel.PartitionDevice(p.Board.Partition())
		if err != nil {
			return p, err
		}
		p.Boot = dev
		return p, nil
	}
	reboot := func() error {
		syscall.Sync()
		return syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "The kernel's trial state and the kernel this slot carries",
		RunE: func(*cobra.Command, []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			fmt.Print(kernel.Status(p))
			return nil
		},
	}
	boot := &cobra.Command{
		Use:   "boot",
		Short: "Take a try for a kernel on trial; put the previous one back when it has none left",
		RunE: func(*cobra.Command, []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			again, err := kernel.Boot(p)
			if err != nil {
				return err
			}
			if again {
				return reboot()
			}
			return nil
		},
	}
	settled := &cobra.Command{
		Use:   "settled",
		Short: "The daemon settled: a kernel on trial is good",
		RunE: func(*cobra.Command, []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			return kernel.Settled(p)
		},
	}
	noroute := &cobra.Command{
		Use:   "noroute",
		Short: "This boot of a kernel on trial had everything but a network: if every try ends so, it is not marked bad",
		RunE: func(*cobra.Command, []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			return kernel.NoRoute(p)
		},
	}
	apply := &cobra.Command{
		Use:   "apply",
		Short: "Write this slot's kernel to its partition when that holds another one, and reboot",
		RunE: func(*cobra.Command, []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			written, err := kernel.Apply(p)
			if err != nil {
				return err
			}
			if written {
				// boot.sh looked just before this, but writing takes a while: an alarm or timer that
				// started meanwhile, or is about to, is let finish first (not forever).
				for end := time.Now().Add(10 * time.Minute); busy.RingsNow(busy.Path) && time.Now().Before(end); {
					time.Sleep(2 * time.Second)
				}
				kernel.Log("rebooting into the new kernel")
				return reboot()
			}
			return nil
		},
	}
	restore := &cobra.Command{
		Use:   "restore",
		Short: "Put the kernel the last update replaced back (from the USB shell), and never write this one again",
		RunE: func(*cobra.Command, []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			if err := kernel.Restore(p); err != nil {
				return err
			}
			kernel.Log("the previous kernel is back; rebooting into it")
			return reboot()
		},
	}
	c.AddCommand(status, boot, settled, noroute, apply, restore)
	return c
}
