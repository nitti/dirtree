.PHONY: build run run-worktree test vet fmt lint clean

build:
	go build $(if $(TAGS),-tags $(TAGS)) -o dirtree ./cmd/dirtree

run: build
	./dirtree $(ARGS)

# Build and run dirtree from a specific worktree, by branch name:
#   make run-worktree BRANCH=<branch> [ARGS=<path>]
# Finds the worktree already checked out for BRANCH (per `git worktree
# list`); if none exists, creates one as a sibling of the repo root
# (../dirtree-<branch>), per CLAUDE.md's worktree convention.
run-worktree:
	@if [ -z "$(BRANCH)" ]; then \
		echo "usage: make run-worktree BRANCH=<branch> [ARGS=<path>]" >&2; \
		exit 1; \
	fi
	@wt=$$(git worktree list --porcelain | awk -v b="refs/heads/$(BRANCH)" '/^worktree /{p=$$2} /^branch /{if ($$2==b) print p}'); \
	if [ -z "$$wt" ]; then \
		wt="$$(dirname $$(git rev-parse --show-toplevel))/dirtree-$(BRANCH)"; \
		echo "No worktree found for branch '$(BRANCH)'; creating one at $$wt" >&2; \
		git worktree add "$$wt" "$(BRANCH)"; \
	fi; \
	echo "Running in worktree: $$wt" >&2; \
	$(MAKE) -C "$$wt" run ARGS="$(ARGS)"

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

lint:
	golangci-lint run

clean:
	rm -f dirtree
