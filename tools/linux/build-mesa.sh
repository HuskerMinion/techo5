#!/bin/sh
# build-mesa.sh — a lean Mesa for the 64-bit userspace of the mainline Show 8 2nd gen and Show 5 2nd
# gen: panfrost (their Mali-G72 and Mali-T720) with EGL, GLES and GBM, and nothing else. Alpine's own
# mesa-dri-gallium links LLVM for its software and AMD drivers, 170 MB that panfrost never uses; this
# one is built without it. Panfrost compiles some of its own GPU programs from OpenCL C when Mesa is
# built, with mesa_clc and a precompiler that need LLVM; so the container builds those two tools first,
# with LLVM, and then the libraries with them and without it. LLVM never leaves the container.
#
# Runs in an arm64 Alpine container (native on an Apple Silicon Mac) from the same Mesa release Alpine
# packages, checked against the sha512 Alpine's APKBUILD pins for that tarball (aports, 3.24-stable,
# main/mesa). Output: bin/techo5-mesa-arm64.tar.gz,
# the files to lay over a 64-bit root filesystem (/usr/lib).
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
VERSION=26.1.6
SHA512=6746b73f5b87e8ff3723eee586b4b5f033dbe6d13cd0309d743b7fa484c350268859ec8ff21aadcbca7a04070cdaa7828146dd28dde4b1935bb879891d687f0b
mkdir -p "$ROOT/bin"
docker run --rm --platform linux/arm64 -e VERSION="$VERSION" -e SHA512="$SHA512" -v "$ROOT/bin:/out" alpine:3.24 sh -ec '
	apk add -q --no-cache build-base meson samurai pkgconf python3 py3-mako py3-yaml py3-packaging \
		bison flex glslang libdrm-dev expat-dev zlib-dev zstd-dev elfutils-dev curl xz \
		llvm-dev clang-dev libclc-dev spirv-llvm-translator-dev spirv-tools-dev >/dev/null
	cd /tmp
	curl -fsSLO "https://mesa.freedesktop.org/archive/mesa-$VERSION.tar.xz"
	echo "$SHA512  mesa-$VERSION.tar.xz" | sha512sum -c -
	tar -xJf "mesa-$VERSION.tar.xz"
	cd "mesa-$VERSION"
	meson setup tools --prefix=/usr --buildtype=release -Dgallium-drivers=panfrost -Dvulkan-drivers= \
		-Dplatforms= -Degl=disabled -Dgbm=disabled -Dopengl=false -Dgles1=disabled -Dgles2=disabled \
		-Dglx=disabled -Dllvm=enabled -Dmesa-clc=enabled -Dinstall-mesa-clc=true \
		-Dprecomp-compiler=enabled -Dinstall-precomp-compiler=true -Dbuild-tests=false \
		>/tmp/tools.log 2>&1 || { tail -25 /tmp/tools.log; exit 1; }
	ninja -C tools >/tmp/tools-ninja.log 2>&1 || { grep -B2 -A8 -m3 error /tmp/tools-ninja.log; exit 1; }
	find tools -type f -perm -u+x \( -name mesa_clc -o -name "*_compile" -o -name vtn_bindgen2 \) -exec cp {} /usr/bin/ \;
	ls /usr/bin | grep -E "mesa_clc|_compile|vtn_bindgen" | tr "\n" " "; echo
	# The libraries need neither LLVM (-Dllvm=disabled) nor SPIRV-Tools, which meson would link if it
	# found it; the tools above still run on both, so their packages stay and only SPIRV-Tools is hidden.
	mkdir -p /tmp/hidden && mv /usr/lib/pkgconfig/SPIRV-Tools*.pc /tmp/hidden/
	meson setup build --prefix=/usr --buildtype=release -Db_ndebug=true \
		-Dgallium-drivers=panfrost -Dvulkan-drivers= -Dllvm=disabled -Dplatforms= \
		-Degl=enabled -Dgles1=disabled -Dgles2=enabled -Dopengl=true -Dglx=disabled -Dgbm=enabled \
		-Dmesa-clc=system -Dprecomp-compiler=system \
		-Dvalgrind=disabled -Dlibunwind=disabled -Dzstd=enabled \
		-Dtools= -Dbuild-tests=false >/tmp/meson.log 2>&1 || { tail -25 /tmp/meson.log; exit 1; }
	ninja -C build >/tmp/ninja.log 2>&1 || { grep -i -B2 -A8 -m3 error /tmp/ninja.log; exit 1; }
	DESTDIR=/stage ninja -C build install >/dev/null
	cd /stage
	find . -name "*.a" -o -name "*.pc" -o -path "*/include/*" | xargs rm -rf
	rm -rf usr/include usr/share/pkgconfig usr/lib/pkgconfig
	find . -type f -name "*.so*" -exec strip --strip-unneeded {} +
	tar -czf /out/techo5-mesa-arm64.tar.gz .
	du -sh /stage; find /stage -type f -name "*.so*" | xargs ls -l | awk "{print \$5, \$9}"
'
