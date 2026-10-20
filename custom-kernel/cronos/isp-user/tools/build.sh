#!/bin/sh
# Build the stand-in libraries, tpipe_test and dpcap for armv7 bionic (Android 11) in Docker.
#
#   tools/build.sh VENDOR_LIB_DIR        -> out/
#
# VENDOR_LIB_DIR holds the unit's libdpframework.so (only linked against, never copied into out/).
# The compiler is arm-none-eabi-gcc (any image with it; the repo's techo5-kbuild has one, override
# with IMAGE=). Bionic's link stubs and crt objects come from the NDK sysroot that
# ../vcodec-user/tools/fetch-deps.sh puts in ../vcodec-user/.cache (CACHE= to move it). The program
# declares the libc functions it uses itself, so no bionic headers are compiled.
set -eu
HERE=$(cd "$(dirname "$0")/.." && pwd)
VENDOR=$(cd "${1:?usage: tools/build.sh VENDOR_LIB_DIR}" && pwd)
CACHE=${CACHE:-$HERE/../vcodec-user/.cache}
CACHE=$(cd "$CACHE" && pwd)
IMAGE=${IMAGE:-techo5-kbuild}

docker run --rm -u "$(id -u):$(id -g)" \
	-v "$HERE:/src" -v "$CACHE:/cache:ro" -v "$VENDOR:/vendor:ro" -w /src \
	"$IMAGE" sh -eu -c '
SR=/cache/ndk/android-ndk-r25c/toolchains/llvm/prebuilt/linux-x86_64/sysroot/usr/lib/arm-linux-androideabi/30
CC="arm-none-eabi-gcc -march=armv7-a -mthumb -mfloat-abi=softfp -mfpu=vfpv3-d16 -fno-short-enums
    -fno-short-wchar -fPIC -ffreestanding -fno-builtin -fno-stack-protector -O2 -Wall -Wextra"
LD="arm-none-eabi-gcc -nostdlib -Wl,-z,now -Wl,-z,noexecstack -Wl,--hash-style=both -Wl,--build-id"
mkdir -p out/lib out/bin out/obj
$CC -c stubs/libsync.c -o out/obj/libsync.o
$CC -c stubs/empty.c -o out/obj/empty.o
$LD -shared -Wl,-soname,libsync.so out/obj/libsync.o -o out/lib/libsync.so
for l in libgralloc_extra libhardware libnativewindow; do
	$LD -shared -Wl,-soname,$l.so out/obj/empty.o -o out/lib/$l.so
done
$CC -c harness/tpipe_test.c -o out/obj/tpipe_test.o
$LD -pie -Wl,--dynamic-linker=/system/bin/linker -Wl,--allow-shlib-undefined \
	$SR/crtbegin_dynamic.o out/obj/tpipe_test.o -L$SR -L/vendor \
	-ldpframework -lc -ldl $SR/crtend_android.o -o out/bin/tpipe_test
arm-none-eabi-readelf -d out/bin/tpipe_test | grep -E "NEEDED|FLAGS|INTERP" || true
# dpcap exports its open() and ioctl(), which the library then calls in place of the libc ones.
$CC -c harness/dpcap.c -o out/obj/dpcap.o
$LD -pie -Wl,--export-dynamic -Wl,--dynamic-linker=/system/bin/linker -Wl,--allow-shlib-undefined \
	$SR/crtbegin_dynamic.o out/obj/dpcap.o -L$SR -L/vendor \
	-ldpframework -lc -ldl $SR/crtend_android.o -o out/bin/dpcap
ls -l out/bin out/lib
'
