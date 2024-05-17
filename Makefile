BASE:=base.tar.gz
DEV_BUILD:=0

GO:=go
GO_FLAGS:=-ldflags "-s -w" # strip Go binaries
CGO_ENABLED:=0
GOMODVENDOR:=

CFLAGS:=-O2 -Wall
LDFLAGS:=-static -s # strip C binaries

GO_FLAGS_EXTRA:=
ifeq "$(GOMODVENDOR)" "1"
GO_FLAGS_EXTRA += -mod=vendor
endif
GO_BUILD_TAGS:=rego
ifneq ($(strip $(GO_BUILD_TAGS)),)
GO_FLAGS_EXTRA += -tags="$(GO_BUILD_TAGS)"
endif
GO_BUILD:=CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GO_FLAGS) $(GO_FLAGS_EXTRA)

SRCROOT=$(dir $(abspath $(firstword $(MAKEFILE_LIST))))
# additional directories to search for rule prerequisites and targets
VPATH=$(SRCROOT)

DELTA_TARGET=out/delta.tar.gz

ifeq "$(DEV_BUILD)" "1"
DELTA_TARGET=out/delta-dev.tar.gz
endif

ifeq "$(SNP_BUILD)" "1"
DELTA_TARGET=out/delta-snp.tar.gz
endif

# The link aliases for gcstools
GCS_TOOLS=\
	generichook \
	install-drivers

# Common path prefix.
PATH_PREFIX:=
# These have PATH_PREFIX prepended to obtain the full path in recipies e.g. $(PATH_PREFIX)/$(VMGS_TOOL)
VMGS_TOOL:=
IGVM_TOOL:=
KERNEL_PATH:=
TAR2EXT4_TOOL:=bin/cmd/tar2ext4

.PHONY: all always rootfs test snp simple combined

.DEFAULT_GOAL := all

BOOTFILE_MAKEFILE_PARAMS:= \
	PATH_PREFIX=$(PATH_PREFIX) \
	VMGS_TOOL=$(VMGS_TOOL) \
	IGVM_TOOL=$(IGVM_TOOL) \
	TAR2EXT4_TOOL=$(TAR2EXT4_TOOL) \
	KERNEL_PATH=$(KERNEL_PATH) \
	DEV_BUILD=$(DEV_BUILD) \
	SNP_BUILD=$(SNP_BUILD)

all: Makefile.bootfiles $(DELTA_TARGET) $(TAR2EXT4_TOOL)
	make -f Makefile.bootfiles $(BOOTFILE_MAKEFILE_PARAMS) all

clean:
	find -name '*.o' -print0 | xargs -0 -r rm
	rm -rf bin deps rootfs out

test:
	cd $(SRCROOT) && $(GO) test -v ./internal/guest/...

rootfs: Makefile.bootfiles $(DELTA_TARGET) $(TAR2EXT4_TOOL)
	make -f Makefile.bootfiles $(BOOTFILE_MAKEFILE_PARAMS) rootfs

snp: Makefile.bootfiles $(DELTA_TARGET) $(TAR2EXT4_TOOL)
	make -f Makefile.bootfiles $(BOOTFILE_MAKEFILE_PARAMS) snp

simple: Makefile.bootfiles $(DELTA_TARGET) $(TAR2EXT4_TOOL)
	make -f Makefile.bootfiles $(BOOTFILE_MAKEFILE_PARAMS) simple
	
combined: Makefile.bootfiles $(DELTA_TARGET) $(TAR2EXT4_TOOL)
	make -f Makefile.bootfiles $(BOOTFILE_MAKEFILE_PARAMS) combined

# This target includes utilities which may be useful for testing purposes.
out/delta-dev.tar.gz: out/delta.tar.gz bin/internal/tools/snp-report
	rm -rf rootfs-dev
	mkdir rootfs-dev
	tar -xzf out/delta.tar.gz -C rootfs-dev
	cp bin/internal/tools/snp-report rootfs-dev/bin/
	tar -zcf $@ -C rootfs-dev .
	rm -rf rootfs-dev

out/delta-snp.tar.gz: out/delta.tar.gz bin/internal/tools/snp-report boot/startup_v2056.sh boot/startup_simple.sh boot/startup.sh
	rm -rf rootfs-snp
	mkdir rootfs-snp
	tar -xzf out/delta.tar.gz -C rootfs-snp
	cp boot/startup_v2056.sh rootfs-snp/startup_v2056.sh
	cp boot/startup_simple.sh rootfs-snp/startup_simple.sh
	cp boot/startup.sh rootfs-snp/startup.sh
	cp bin/internal/tools/snp-report rootfs-snp/bin/
	chmod a+x rootfs-snp/startup_v2056.sh
	chmod a+x rootfs-snp/startup_simple.sh
	chmod a+x rootfs-snp/startup.sh
	tar -zcf $@ -C rootfs-snp .
	rm -rf rootfs-snp

out/delta.tar.gz: bin/init bin/vsockexec bin/cmd/gcs bin/cmd/gcstools bin/cmd/hooks/wait-paths Makefile
	@mkdir -p out
	rm -rf rootfs
	mkdir -p rootfs/bin/
	mkdir -p rootfs/info/
	cp bin/init rootfs/
	cp bin/vsockexec rootfs/bin/
	cp bin/cmd/gcs rootfs/bin/
	cp bin/cmd/gcstools rootfs/bin/
	cp bin/cmd/hooks/wait-paths rootfs/bin/
	for tool in $(GCS_TOOLS); do ln -s gcstools rootfs/bin/$$tool; done
	git -C $(SRCROOT) rev-parse HEAD > rootfs/info/gcs.commit && \
	git -C $(SRCROOT) rev-parse --abbrev-ref HEAD > rootfs/info/gcs.branch && \
	date --iso-8601=minute --utc > rootfs/info/tar.date
	$(if $(and $(realpath $(subst .tar,.testdata.json,$(BASE))), $(shell which jq)), \
		jq -r '.IMAGE_NAME' $(subst .tar,.testdata.json,$(BASE)) 2>/dev/null > rootfs/info/image.name && \
		jq -r '.DATETIME' $(subst .tar,.testdata.json,$(BASE)) 2>/dev/null > rootfs/info/build.date)
	tar -zcf $@ -C rootfs .
	rm -rf rootfs

bin/cmd/gcs bin/cmd/gcstools bin/cmd/hooks/wait-paths bin/cmd/tar2ext4 bin/internal/tools/snp-report:
	@mkdir -p $(dir $@)
	GOOS=linux $(GO_BUILD) -o $@ $(SRCROOT)/$(@:bin/%=%)

bin/vsockexec: vsockexec/vsockexec.o vsockexec/vsock.o
	@mkdir -p bin
	$(CC) $(LDFLAGS) -o $@ $^

bin/init: init/init.o vsockexec/vsock.o
	@mkdir -p bin
	$(CC) $(LDFLAGS) -o $@ $^

%.o: %.c
	@mkdir -p $(dir $@)
	$(CC) $(CFLAGS) $(CPPFLAGS) -c -o $@ $<
