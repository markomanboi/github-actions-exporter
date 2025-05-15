#!/usr/bin/env bash

set -x # Keep this for debugging build steps

# Ensure dependencies are tidy and go.sum is up to date
echo "Running go mod tidy..."
go mod tidy
if [ $? -ne 0 ]; then
  echo "go mod tidy failed, exiting."
  exit 1
fi
echo "go mod tidy completed."

VERSION_BRANCH=`git branch --show-current 2>/dev/null` # More robust way to get current branch
VERSION_TAG=`git describe --tags --abbrev=0 2>/dev/null` # Get latest tag

VERSION=$VERSION_BRANCH
if [[ "$VERSION_BRANCH" == "master" ]] || [[ "$VERSION_BRANCH" == "main" ]] || [[ -z "$VERSION_BRANCH" && -n "$VERSION_TAG" ]];
then
  if [[ -n "$VERSION_TAG" ]]; then
    VERSION=$VERSION_TAG
  elif [[ -z "$VERSION_BRANCH" ]]; then # If neither branch nor tag, use a default or commit hash
    VERSION=$(git rev-parse --short HEAD 2>/dev/null || echo "dev")
  fi
elif [[ -z "$VERSION_BRANCH" && -z "$VERSION_TAG" ]]; then # If in detached HEAD state and no tags
    VERSION=$(git rev-parse --short HEAD 2>/dev/null || echo "dev")
fi


echo "Building version: $VERSION"

# Ensure the bin directory exists
mkdir -p bin

# Build the application
CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-X 'main.version=$VERSION'" -v -o bin/app .
# Added -v for verbose build output
# Added . at the end to specify current directory as the package to build (if your main package is there)
# Or specify the path to your main package e.g., ./cmd/exporter

if [ $? -eq 0 ]; then
  echo "Build successful: bin/app"
else
  echo "Build failed."
  exit 1
fi