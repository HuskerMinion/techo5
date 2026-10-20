# Builds the stub libraries and the test harnesses for armv7 bionic (API 30)
# with Debian clang/lld and the NDK sysroot. Run it through tools/build.sh,
# which runs make inside the vcodec-user-build container.
#
#   make CACHE=/path/to/.cache            -> out/
#   make CACHE=... VENDOR=/path/to/vendor/lib stage   -> out/staging/

CACHE    ?= $(CURDIR)/.cache
VENDOR   ?= $(CURDIR)/vendor-lib
NDK      := $(CACHE)/ndk/android-ndk-r25c/toolchains/llvm/prebuilt/linux-x86_64
OUT      ?= $(CURDIR)/out
RD       := $(OUT)/.resource-dir

CLANG_RES := $(shell clang -print-resource-dir 2>/dev/null)
TARGET   := --target=armv7a-linux-androideabi30 --sysroot=$(NDK)/sysroot -resource-dir $(RD)
CFLAGS   := $(TARGET) -O2 -g -fPIC -Wall -Wextra -Wno-unused-parameter -mthumb \
            -fvisibility=hidden -Iinclude
CXXFLAGS := $(CFLAGS) -std=c++17 -fno-exceptions -fno-rtti
LDFLAGS  := $(TARGET) -fuse-ld=lld -rtlib=compiler-rt -unwindlib=none \
            -Wl,--hash-style=both -Wl,-z,relro -Wl,-z,now -Wl,--no-undefined

STUBS := liblog.so libcutils.so libion.so libion_mtk.so libm4u.so libbwc.so libutils.so \
         libmp4enc_sa.ca7.so libvc1dec_sa.ca7.so libvp8enc_sa.ca7.so libhevce_sb.ca7.android.so \
         libfakevcodec.so
BINS  ?= smoke_test vdec_test venc_test vcodec_dec vcodec_enc

all: $(addprefix $(OUT)/lib/,$(STUBS)) $(addprefix $(OUT)/bin/,$(BINS))

$(RD)/.ok:
	mkdir -p $(RD)/lib
	ln -sfn $(CLANG_RES)/include $(RD)/include
	ln -sfn $(NDK)/lib64/clang/14.0.7/lib/linux $(RD)/lib/linux
	touch $@

$(OUT)/lib/%.o: stubs/%.c stubs/vcshim.h include/vcodec_shim_uapi.h $(RD)/.ok
	@mkdir -p $(dir $@)
	clang $(CFLAGS) -c $< -o $@
$(OUT)/lib/%.o: stubs/%.cpp stubs/vcshim.h $(RD)/.ok
	@mkdir -p $(dir $@)
	clang++ $(CXXFLAGS) -c $< -o $@

SOFLAGS = -shared -Wl,-soname,$(notdir $@)

$(OUT)/lib/liblog.so: $(OUT)/lib/liblog.o
	clang $(LDFLAGS) $(SOFLAGS) $^ -o $@
$(OUT)/lib/libcutils.so: $(OUT)/lib/libcutils.o
	clang $(LDFLAGS) $(SOFLAGS) $^ -o $@
$(OUT)/lib/libion.so: $(OUT)/lib/libion.o
	clang $(LDFLAGS) $(SOFLAGS) $^ -o $@
$(OUT)/lib/libion_mtk.so: $(OUT)/lib/libion_mtk.o $(OUT)/lib/libion.so
	clang $(LDFLAGS) $(SOFLAGS) $< -L$(OUT)/lib -lion -o $@
$(OUT)/lib/libm4u.so: $(OUT)/lib/libm4u.o $(OUT)/lib/libion.so
	clang++ $(LDFLAGS) -nostdlib++ $(SOFLAGS) $< -L$(OUT)/lib -lion -o $@
# test only (LD_PRELOAD under qemu), see stubs/fakevcodec.c
$(OUT)/lib/libfakevcodec.so: $(OUT)/lib/fakevcodec.o
	clang $(LDFLAGS) $(SOFLAGS) $^ -o $@
$(OUT)/lib/libbwc.so: $(OUT)/lib/libbwc.o
	clang++ $(LDFLAGS) -nostdlib++ $(SOFLAGS) $< -o $@
$(OUT)/lib/libutils.so $(OUT)/lib/libmp4enc_sa.ca7.so $(OUT)/lib/libvc1dec_sa.ca7.so \
$(OUT)/lib/libvp8enc_sa.ca7.so $(OUT)/lib/libhevce_sb.ca7.android.so: $(OUT)/lib/empty.o
	clang $(LDFLAGS) $(SOFLAGS) $^ -o $@

# Harnesses link against the vendor libvcodecdrv/libvcodec_utility for real
# (the NEEDED entries are then resolved at runtime from /vendor/lib).
HARN_LDFLAGS = $(LDFLAGS) -pie -Wl,--allow-shlib-undefined -L$(VENDOR) -L$(OUT)/lib

$(OUT)/bin/%: harness/%.c harness/common.h include/*.h $(addprefix $(OUT)/lib/,$(STUBS))
	@mkdir -p $(dir $@)
	clang $(CFLAGS) -fPIE $< harness/common.c $(HARN_LDFLAGS) -lvcodecdrv -lvcodec_utility -llog -o $@

# ---- staging tree (copy to the device, run with chroot) ----
A11 := $(CACHE)/android11/system
ST  := $(OUT)/staging
stage: all
	rm -rf $(ST)
	mkdir -p $(ST)/system/bin $(ST)/system/lib $(ST)/vendor/lib $(ST)/vendor/etc \
	         $(ST)/dev $(ST)/proc $(ST)/data/local/tmp
	cp $(A11)/apex/com.android.runtime/bin/linker $(ST)/system/bin/linker
	cp $(A11)/apex/com.android.runtime/lib/bionic/libc.so \
	   $(A11)/apex/com.android.runtime/lib/bionic/libm.so \
	   $(A11)/apex/com.android.runtime/lib/bionic/libdl.so \
	   $(A11)/lib/ld-android.so \
	   $(A11)/lib/libc++.so $(ST)/system/lib/
	cp $(OUT)/lib/liblog.so $(OUT)/lib/libcutils.so $(OUT)/lib/libutils.so $(ST)/system/lib/
	cp $(OUT)/lib/libion.so $(OUT)/lib/libfakevcodec.so $(ST)/system/lib/
	cp $(OUT)/lib/libion_mtk.so $(OUT)/lib/libm4u.so $(OUT)/lib/libbwc.so \
	   $(OUT)/lib/libmp4enc_sa.ca7.so $(OUT)/lib/libvc1dec_sa.ca7.so \
	   $(OUT)/lib/libvp8enc_sa.ca7.so $(OUT)/lib/libhevce_sb.ca7.android.so $(ST)/vendor/lib/
	cp $(VENDOR)/libvcodecdrv.so $(VENDOR)/libvcodec_utility.so $(VENDOR)/libvcodec_oal.so \
	   $(VENDOR)/libmtk_drvb.so $(ST)/vendor/lib/
	cp $(addprefix $(OUT)/bin/,$(BINS)) $(ST)/system/bin/
	cp etc/vcodec.prop $(ST)/vendor/etc/vcodec.prop
	cp tools/run-chroot.sh tools/run-direct.sh $(ST)/
	chmod 755 $(ST)/system/bin/* $(ST)/run-*.sh
	@echo "staging tree: $(ST)"

clean:
	rm -rf $(OUT)

.PHONY: all stage clean
