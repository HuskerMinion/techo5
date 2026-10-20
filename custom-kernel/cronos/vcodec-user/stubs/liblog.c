/*
 * Minimal liblog.so: Android log calls go to stderr. There is no logd, and
 * AOSP's liblog would silently drop everything.
 *
 * VCODEC_LOG=V|D|I|W|E|F|S (or 2..8) sets the minimum priority (default I).
 * Note: the vendor libs log many routine messages at ERROR priority.
 */
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>
#include <sys/syscall.h>

#define EXPORT __attribute__((visibility("default")))

static int min_prio = -1;

static int get_min(void)
{
	if (min_prio < 0) {
		const char *e = getenv("VCODEC_LOG");
		min_prio = 4;
		if (e && *e) {
			const char *l = "VDIWEFS";
			const char *p = strchr(l, *e);
			min_prio = p ? (int)(p - l) + 2 : atoi(e);
		}
	}
	return min_prio;
}

static int vlog(int prio, const char *tag, const char *fmt, va_list ap)
{
	if (prio < get_min())
		return 0;
	struct timespec ts;
	char buf[1024];
	int n;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	n = vsnprintf(buf, sizeof(buf), fmt, ap);
	while (n > 0 && n < (int)sizeof(buf) && (buf[n - 1] == '\n'))
		buf[--n] = 0;
	fprintf(stderr, "%5ld.%06ld %5ld %c %s: %s\n", (long)ts.tv_sec, ts.tv_nsec / 1000,
		(long)syscall(__NR_gettid), "??VDIWEFS"[prio > 8 || prio < 0 ? 0 : prio],
		tag ? tag : "", buf);
	return n;
}

EXPORT int __android_log_print(int prio, const char *tag, const char *fmt, ...)
{
	va_list ap;
	va_start(ap, fmt);
	int r = vlog(prio, tag, fmt, ap);
	va_end(ap);
	return r;
}

EXPORT int __android_log_vprint(int prio, const char *tag, const char *fmt, va_list ap)
{
	return vlog(prio, tag, fmt, ap);
}

EXPORT int __android_log_write(int prio, const char *tag, const char *msg)
{
	return __android_log_print(prio, tag, "%s", msg);
}

EXPORT int __android_log_buf_write(int buf, int prio, const char *tag, const char *msg)
{
	(void)buf;
	return __android_log_write(prio, tag, msg);
}

EXPORT int __android_log_buf_print(int buf, int prio, const char *tag, const char *fmt, ...)
{
	(void)buf;
	va_list ap;
	va_start(ap, fmt);
	int r = vlog(prio, tag, fmt, ap);
	va_end(ap);
	return r;
}

EXPORT void __android_log_assert(const char *cond, const char *tag, const char *fmt, ...)
{
	if (fmt) {
		va_list ap;
		va_start(ap, fmt);
		vlog(7, tag, fmt, ap);
		va_end(ap);
	} else {
		__android_log_print(7, tag, "assertion failed: %s", cond ? cond : "");
	}
	abort();
}

EXPORT int __android_log_is_loggable(int prio, const char *tag, int def)
{
	(void)tag; (void)def;
	return prio >= get_min();
}
