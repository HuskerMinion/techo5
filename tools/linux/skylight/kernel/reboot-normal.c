// SPDX-License-Identifier: MIT
/*
 * Restart with the reboot-mode "normal" command, so the Rockchip boot mode register says normal.
 *
 * Built without a C library (build.sh: -nostdlib -ffreestanding, its own _start), so the bench and
 * installer images carry nothing of one: two system calls are all it makes. arm64 only.
 */
#include <asm/unistd.h>
#include <linux/reboot.h>

static long sys4(long n, long a, long b, long c, long d)
{
	register long x8 __asm__("x8") = n;
	register long x0 __asm__("x0") = a;
	register long x1 __asm__("x1") = b;
	register long x2 __asm__("x2") = c;
	register long x3 __asm__("x3") = d;

	__asm__ volatile("svc #0" : "+r"(x0) : "r"(x8), "r"(x1), "r"(x2), "r"(x3) : "memory");
	return x0;
}

void _start(void)
{
	static const char mode[] = "normal";

	sys4(__NR_sync, 0, 0, 0, 0);
	sys4(__NR_reboot, LINUX_REBOOT_MAGIC1, LINUX_REBOOT_MAGIC2, LINUX_REBOOT_CMD_RESTART2, (long)mode);
	/* Only when the reboot was refused: the callers fall back to busybox's reboot. */
	sys4(__NR_exit, 1, 0, 0, 0);
	for (;;)
		;
}
