#!/usr/bin/env sh
set -eu

# Build the custom reverse_ssh Docker image locally.
# The source can be cloned by this script or prepared manually beforehand.

ENV_FILE="${ENV_FILE:-./.env}"
if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi

REVERSE_SSH_SOURCE_DIR="${REVERSE_SSH_SOURCE_DIR:-/opt/reverse-logger/src/reverse_ssh}"
REVERSE_SSH_IMAGE="${REVERSE_SSH_IMAGE:-reverse-ssh:local}"
REVERSE_SSH_BUILD_CONTEXT="${REVERSE_SSH_BUILD_CONTEXT:-$REVERSE_SSH_SOURCE_DIR}"
REVERSE_SSH_DOCKERFILE="${REVERSE_SSH_DOCKERFILE:-$REVERSE_SSH_BUILD_CONTEXT/Dockerfile}"

command -v docker >/dev/null
command -v git >/dev/null

if ! docker info >/dev/null 2>&1; then
  echo "docker is not reachable by the current user." >&2
  echo "Add the deploy user to the docker group or run this helper in a shell with Docker access." >&2
  exit 1
fi

if [ ! -d "$REVERSE_SSH_SOURCE_DIR/.git" ]; then
  if [ "${REVERSE_SSH_REPO_URL:-}" ]; then
    mkdir -p "$(dirname "$REVERSE_SSH_SOURCE_DIR")"
    git clone "$REVERSE_SSH_REPO_URL" "$REVERSE_SSH_SOURCE_DIR"
  else
    echo "reverse_ssh source is missing: $REVERSE_SSH_SOURCE_DIR" >&2
    echo "Set REVERSE_SSH_REPO_URL or clone the repository there manually." >&2
    exit 1
  fi
fi

if [ "${REVERSE_SSH_REPO_REF:-}" ]; then
  git -C "$REVERSE_SSH_SOURCE_DIR" fetch --all --tags
  git -C "$REVERSE_SSH_SOURCE_DIR" checkout "$REVERSE_SSH_REPO_REF"
fi

if [ ! -f "$REVERSE_SSH_DOCKERFILE" ]; then
  echo "reverse_ssh Dockerfile not found: $REVERSE_SSH_DOCKERFILE" >&2
  echo "Set REVERSE_SSH_DOCKERFILE and REVERSE_SSH_BUILD_CONTEXT if the repo uses a custom layout." >&2
  exit 1
fi

docker build \
  -t "$REVERSE_SSH_IMAGE" \
  -f "$REVERSE_SSH_DOCKERFILE" \
  "$REVERSE_SSH_BUILD_CONTEXT"

echo "Built reverse_ssh image: $REVERSE_SSH_IMAGE"
