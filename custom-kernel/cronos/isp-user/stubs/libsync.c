/*
 * Stand-in libsync.so. libdpframework imports the fence helpers for its MDP
 * paths; the tile driver never calls them. Every call fails cleanly.
 */
#define EXPORT __attribute__((visibility("default")))

EXPORT int sync_wait(int fd, int timeout) { (void)fd; (void)timeout; return -1; }
EXPORT void *sync_fence_info(int fd) { (void)fd; return 0; }
EXPORT void *sync_pt_info(void *info, void *itr) { (void)info; (void)itr; return 0; }
EXPORT void sync_fence_info_free(void *info) { (void)info; }
