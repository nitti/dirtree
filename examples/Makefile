# Downloads real-world repos (as plain ZIP snapshots, no git history) and
# generates synthetic large files, for manually exercising dirtree against
# trees much bigger than this repo itself. Everything lands under data/,
# which is .gitignore'd in full — nothing fetched or generated here is ever
# meant to be committed to dirtree's own history.

DATA_DIR := data

.PHONY: all list clean \
	linux clean-linux \
	llvm clean-llvm \
	cpython clean-cpython \
	bigfiles clean-bigfiles \
	bench-linux bench-bigfiles compare

all: linux llvm cpython bigfiles

list:
	@echo "Targets:"
	@echo "  make linux         - Linux kernel source (torvalds/linux, ~80k files, deep nesting)"
	@echo "  make llvm          - LLVM monorepo (llvm/llvm-project, larger and deeper than linux)"
	@echo "  make cpython       - CPython source (python/cpython, smaller, quick to fetch)"
	@echo "  make bigfiles      - synthetic large files with known search markers (see README.md)"
	@echo "  make all           - all of the above"
	@echo "  make bench-linux   - fetch linux (if needed) and benchmark content search's concurrency knob against it"
	@echo "  make bench-bigfiles - generate bigfiles (if needed) and benchmark against it"
	@echo "  make compare DIR=<path> QUERY=<term> - time dirtree vs ripgrep/grep/ag over the same directory/query"
	@echo "  make clean         - remove everything under data/"
	@echo "  make clean-<name>  - remove just that one target's data"

# --- linux ---

linux: $(DATA_DIR)/linux/.fetched

$(DATA_DIR)/linux/.fetched:
	@mkdir -p $(DATA_DIR)
	curl -fL "https://codeload.github.com/torvalds/linux/zip/refs/heads/master" -o $(DATA_DIR)/linux.zip
	rm -rf $(DATA_DIR)/linux
	unzip -qo $(DATA_DIR)/linux.zip -d $(DATA_DIR)
	mv $(DATA_DIR)/linux-master $(DATA_DIR)/linux
	rm $(DATA_DIR)/linux.zip
	touch $@

clean-linux:
	rm -rf $(DATA_DIR)/linux

# --- llvm ---

llvm: $(DATA_DIR)/llvm/.fetched

$(DATA_DIR)/llvm/.fetched:
	@mkdir -p $(DATA_DIR)
	curl -fL "https://codeload.github.com/llvm/llvm-project/zip/refs/heads/main" -o $(DATA_DIR)/llvm.zip
	rm -rf $(DATA_DIR)/llvm
	unzip -qo $(DATA_DIR)/llvm.zip -d $(DATA_DIR)
	mv $(DATA_DIR)/llvm-project-main $(DATA_DIR)/llvm
	rm $(DATA_DIR)/llvm.zip
	touch $@

clean-llvm:
	rm -rf $(DATA_DIR)/llvm

# --- cpython ---

cpython: $(DATA_DIR)/cpython/.fetched

$(DATA_DIR)/cpython/.fetched:
	@mkdir -p $(DATA_DIR)
	curl -fL "https://codeload.github.com/python/cpython/zip/refs/heads/main" -o $(DATA_DIR)/cpython.zip
	rm -rf $(DATA_DIR)/cpython
	unzip -qo $(DATA_DIR)/cpython.zip -d $(DATA_DIR)
	mv $(DATA_DIR)/cpython-main $(DATA_DIR)/cpython
	rm $(DATA_DIR)/cpython.zip
	touch $@

clean-cpython:
	rm -rf $(DATA_DIR)/cpython

# --- bigfiles ---

bigfiles: $(DATA_DIR)/bigfiles/.generated

$(DATA_DIR)/bigfiles/.generated: make_bigfiles.sh
	@mkdir -p $(DATA_DIR)/bigfiles
	./make_bigfiles.sh $(DATA_DIR)/bigfiles
	touch $@

clean-bigfiles:
	rm -rf $(DATA_DIR)/bigfiles

clean:
	rm -rf $(DATA_DIR)

# --- bench / compare ---

bench-linux: linux
	DIRTREE_BENCH_DIR=$(CURDIR)/$(DATA_DIR)/linux $(MAKE) -C .. bench

bench-bigfiles: bigfiles
	DIRTREE_BENCH_DIR=$(CURDIR)/$(DATA_DIR)/bigfiles $(MAKE) -C .. bench

compare:
	@if [ -z "$(DIR)" ] || [ -z "$(QUERY)" ]; then \
		echo "usage: make compare DIR=<path> QUERY=<term>" >&2; \
		exit 1; \
	fi
	./compare_search.sh "$(DIR)" "$(QUERY)"
