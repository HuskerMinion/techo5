/*
 * Minimal libcutils.so for the vendor codec libs. Imports covered:
 *   property_get                            (libvcodecdrv, libvcodec_utility)
 *   atrace_setup/_begin_body/_end_body/_int_body,
 *   atrace_enabled_tags, atrace_is_ready    (libvcodecdrv)
 *   ashmem_create_region, ashmem_set_prot_region (libvcodec_utility; only
 *                                           reached from OMX paths)
 * No Android property service is needed. Properties come from a key=value
 * file, $VCODEC_PROP_FILE (default /vendor/etc/vcodec.prop), and then from
 * $VCODEC_PROPS ("k=v;k=v", which wins). Set VCODEC_SHIM_DEBUG=1 to log
 * every lookup.
 * Useful keys: vendor.mtk.vdec.h264.log, vendor.mtk.venc.h264.showlog,
 * vendor.mtk.vdec.regdump (see README).
 */
#include <errno.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/syscall.h>
#include <unistd.h>

#define EXPORT __attribute__((visibility("default")))
#define PROP_VALUE_MAX 92

struct prop { char *k, *v; struct prop *next; };
static struct prop *g_props;
static pthread_once_t g_once = PTHREAD_ONCE_INIT;

static void add_prop(const char *k, size_t kl, const char *v, size_t vl)
{
	while (kl && (k[kl - 1] == ' ' || k[kl - 1] == '\t')) kl--;
	while (vl && (v[vl - 1] == ' ' || v[vl - 1] == '\r' || v[vl - 1] == '\n')) vl--;
	if (!kl)
		return;
	struct prop *p = calloc(1, sizeof(*p));
	p->k = strndup(k, kl);
	p->v = strndup(v, vl < PROP_VALUE_MAX - 1 ? vl : PROP_VALUE_MAX - 1);
	p->next = g_props; /* later entries shadow earlier ones */
	g_props = p;
}

static void parse_kv(const char *s, const char *seps)
{
	while (*s) {
		size_t n = strcspn(s, seps);
		const char *eq = memchr(s, '=', n);
		while (*s == ' ' || *s == '\t') { s++; n--; }
		if (eq && *s != '#')
			add_prop(s, eq - s, eq + 1, n - (eq + 1 - s));
		s += n;
		if (*s) s++;
	}
}

static void load(void)
{
	const char *f = getenv("VCODEC_PROP_FILE");
	FILE *fp = fopen(f ? f : "/vendor/etc/vcodec.prop", "re");
	if (fp) {
		char line[512];
		while (fgets(line, sizeof(line), fp))
			parse_kv(line, "\n");
		fclose(fp);
	}
	if (getenv("VCODEC_PROPS"))
		parse_kv(getenv("VCODEC_PROPS"), ";");
}

EXPORT int property_get(const char *key, char *value, const char *default_value)
{
	pthread_once(&g_once, load);
	const char *v = default_value ? default_value : "";
	for (struct prop *p = g_props; p; p = p->next)
		if (!strcmp(p->k, key)) { v = p->v; break; }
	size_t n = strlen(v);
	if (n >= PROP_VALUE_MAX)
		n = PROP_VALUE_MAX - 1;
	memcpy(value, v, n);
	value[n] = 0;
	const char *d = getenv("VCODEC_SHIM_DEBUG");
	if (d && atoi(d) >= 1)
		fprintf(stderr, "[vcshim] property_get(%s) = \"%s\"\n", key, value);
	return (int)n;
}

EXPORT int property_set(const char *key, const char *value)
{
	(void)key; (void)value;
	return 0;
}

EXPORT int8_t property_get_bool(const char *key, int8_t def)
{
	char v[PROP_VALUE_MAX];
	if (property_get(key, v, NULL) == 0) return def;
	if (!strcmp(v, "1") || !strcmp(v, "true") || !strcmp(v, "y") || !strcmp(v, "yes") || !strcmp(v, "on")) return 1;
	if (!strcmp(v, "0") || !strcmp(v, "false") || !strcmp(v, "n") || !strcmp(v, "no") || !strcmp(v, "off")) return 0;
	return def;
}

EXPORT int64_t property_get_int64(const char *key, int64_t def)
{
	char v[PROP_VALUE_MAX], *e;
	if (property_get(key, v, NULL) == 0) return def;
	long long r = strtoll(v, &e, 0);
	return *e ? def : r;
}

EXPORT int32_t property_get_int32(const char *key, int32_t def)
{
	return (int32_t)property_get_int64(key, def);
}

/* ---- atrace: tracing permanently disabled ---- */
EXPORT atomic_bool atrace_is_ready = true;
EXPORT uint64_t atrace_enabled_tags = 0;
EXPORT int atrace_marker_fd = -1;
EXPORT void atrace_setup(void) {}
EXPORT void atrace_begin_body(const char *name) { (void)name; }
EXPORT void atrace_end_body(void) {}
EXPORT void atrace_int_body(const char *name, int32_t v) { (void)name; (void)v; }
EXPORT void atrace_int64_body(const char *name, int64_t v) { (void)name; (void)v; }
EXPORT void atrace_async_begin_body(const char *n, int32_t c) { (void)n; (void)c; }
EXPORT void atrace_async_end_body(const char *n, int32_t c) { (void)n; (void)c; }
EXPORT void atrace_update_tags(void) {}
EXPORT void atrace_set_tracing_enabled(int e) { (void)e; }
EXPORT uint64_t atrace_get_enabled_tags(void) { return 0; }

/* ---- ashmem via memfd (mainline has no /dev/ashmem) ---- */
EXPORT int ashmem_create_region(const char *name, size_t size)
{
	int fd = syscall(__NR_memfd_create, name ? name : "ashmem", 1 /* MFD_CLOEXEC */);
	if (fd < 0)
		return -1;
	if (ftruncate(fd, size) < 0) {
		close(fd);
		return -1;
	}
	return fd;
}
EXPORT int ashmem_set_prot_region(int fd, int prot) { (void)fd; (void)prot; return 0; }
EXPORT int ashmem_pin_region(int fd, size_t o, size_t l) { (void)fd; (void)o; (void)l; return 0; }
EXPORT int ashmem_unpin_region(int fd, size_t o, size_t l) { (void)fd; (void)o; (void)l; return 0; }
EXPORT int ashmem_get_size_region(int fd)
{
	off_t s = lseek(fd, 0, SEEK_END);
	return s < 0 ? -1 : (int)s;
}
EXPORT int ashmem_valid(int fd) { return fd >= 0; }
