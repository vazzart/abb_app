.PHONY: build test prepare-addon

build:
	CGO_ENABLED=0 go build -o abb ./cmd/abb

test:
	go test ./...

# Sync Go source into the HA add-on directory before committing or building.
# Run this after any changes to cmd/, config/, or internal/.
prepare-addon:
	rsync -av --delete --relative \
	    --exclude='*.db' \
	    --exclude='*.test' \
	    cmd config internal go.mod go.sum \
	    android_bridge_bot/src/
	@echo "Sources synced to android_bridge_bot/src/"
